// worker.go is the worker role (HH-3.2 fold, D37): every background loop
// that writes starts here and ONLY here. The events_outbox dispatcher, the
// sms_outbox dispatcher, the jobs runner with its registered jobs, the
// messaging retention sweep and the approvals reconciler are carried
// UNCHANGED in behaviour (the loops, their intervals, their claim
// semantics and their panic-on-duplicate rules are the pin's); only their
// startup location changes, from the pin's single main to this role. The
// worker builds the SAME wiring graph the request path uses, over a
// discard registrar, so the loops' subjects are the instances serve would
// serve and no wiring decision is written twice.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/jobs"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messagingprovider"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/portal"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/jackc/pgx/v5/pgxpool"
)

// claimStores are the two outbox stores the worker's dispatchers drain through. Each claim loop holds
// its claimed row in an open transaction while the registered handlers run, and eight of the ten
// handlers ask the shared pool for a SECOND connection inside that window. The stores therefore get
// their own claim connection source (buildClaimStores), so a dispatcher's held claim never takes one
// of the shared pool's connections. The enqueue and requeue paths keep the shared pool.
type claimStores struct {
	events *notify.PgOutboxStore
	sms    *messagingprovider.Outbox
}

// buildClaimStores is the worker's one point where the two claim stores are built.
func buildClaimStores(ctx context.Context, shared *pgxpool.Pool, policy *messagingprovider.SendPolicy, log *slog.Logger) (*claimStores, error) {
	_ = ctx
	return &claimStores{
		events: notify.NewPgOutboxStore(shared),
		sms:    messagingprovider.NewOutbox(shared).WithSendPolicy(policy).WithLogger(log),
	}, nil
}

// Close releases whatever pools the stores own.
func (c *claimStores) Close() {}

// worker builds the graph, registers every loop's handlers in the pin's
// order (the MUST-run-AFTER ordering the pin's comments carry: the
// observers register only after their topics' primary owners), starts the
// loops, and blocks until the process is signalled.
func worker(getenv func(string) string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	graph, err := registerRoutes(discardRegistrar{}, getenv)
	if err != nil {
		return err
	}
	defer graph.pool.Close()
	log := graph.log
	pool := graph.pool

	// F0.2: the events_outbox dispatcher. The portal emits invite and
	// password-reset signals inside the business transaction; this
	// dispatcher drains them and delivers through the Notifier port (the
	// dev log stub unless a live provider is configured; the loud warning
	// lives in the tops wiring where the notifier is built).
	claims, err := buildClaimStores(ctx, pool, graph.tops.sms.policy, log)
	if err != nil {
		return err
	}
	defer claims.Close()
	dispatcher := notify.NewDispatcher(claims.events, log)
	portal.RegisterNotificationHandlers(dispatcher, graph.tops.notifier, graph.cfg.PortalBaseURL, log)
	// W5 A-01: the GL posting consumer owns all eight financial topics.
	graph.money.glSvc.RegisterPostingHandlers(dispatcher)
	// W6 INV-CORE: po.received + inventory.adjusted (disjoint from the
	// eight; the dispatcher PANICS on a duplicate topic registration).
	graph.money.inventorySvc.RegisterPostingHandlers(dispatcher)
	// DIB-191: the two NON-OWNING order observers, registering AFTER the GL
	// consumer so both observed topics already have their primary owner.
	order.NewLifecycleConsumer(graph.money.orderSvc, graph.tops.billingRepo, log).RegisterHandlers(dispatcher)
	// MSG-01 FR-3: the messaging consumer OWNS msg.event.* and OBSERVES the
	// raw domain topics; MUST run AFTER the gl/inventory registrations.
	messaging.NewConsumer(graph.ops.messagingSvc, log).RegisterHandlers(dispatcher)
	// The contractor-portal inbound bridge observes crm.NewPortalContact,
	// owned by portal.RegisterNotificationHandlers above (D-026).
	messaging.NewPortalBridge(graph.ops.messagingSvc, log).RegisterHandlers(dispatcher)
	go dispatcher.Run(ctx) // stops on ctx cancellation (SIGINT/SIGTERM)

	// 1 · OUT-OF-BAND NOTIFICATION RUNS ON ITS OWN DISPATCHER, DELIBERATELY
	// (the pin's two-dispatcher separation rationale, D37's verbatim
	// carry): events_outbox claims by STATUS alone, so a new topic on the
	// shared dispatcher would race the frozen handler map; sms_outbox is a
	// disjoint table, and a carrier outage cannot touch a GL journal.
	smsDispatcher := notify.NewDispatcher(claims.sms, log)
	registerSmsConsumers(graph.tops.sms, graph.tops.notifier, smsDispatcher, log)
	go smsDispatcher.Run(ctx)

	// MSG-01 Feature B: the retention sweep (idempotent archival, never a
	// delete), stops on ctx cancel.
	retention := messaging.NewRetentionWorker(graph.ops.messagingRepo, log)
	go retention.Run(ctx)

	// The approvals rail's 60s reconcile sweep (backfills missing cards and
	// decision stamps; no-op when the notifier is unwired).
	reconciler := messaging.NewReconcileWorker(graph.ops.messagingSvc, approvalSourceAdapter{svc: graph.money.approvals}, log)
	go reconciler.Run(ctx)

	// The scheduler (0096). Every registration BEFORE Run where the pin
	// had it so (the runner's handler map is written by Register and read
	// by the tick loop); the two W3-D registrations follow Run in the
	// pin's own order and wording.
	unionCreditSweep := billing.CreditHoldSweepJob(graph.tops.creditSvc, "")
	creditMemoRepost := billing.CreditMemoRepostJob(
		billing.NewInvoiceWriter(pool).
			WithCreditMemoPoster(w6cCreditMemoPoster{svc: graph.money.glSvc, rules: gl.NewRepository(pool)}),
		0) // 0 = the package's batch ceiling
	jobRunner := jobs.NewRunner(jobs.NewRepository(pool), log).
		Register("ar.credit_hold_sweep", unionCreditSweep).
		Register(billing.CreditMemoRepostJobName, creditMemoRepost).
		Register("warranty.sla.sweep", func(jobCtx context.Context) (jobs.Result, error) {
			// The SLA sweep runs under a GLOBAL system identity:
			// SweepSLA resolves a StaffContext and branch-scopes; the
			// nightly sweep sees every branch.
			sysCtx := identity.ContextWithStaff(jobCtx, identity.StaffContext{
				UserID: "system:jobs", Scope: identity.ScopeGlobal, Status: identity.StatusActive,
			})
			raised, err := graph.ops.warrantySvc.SweepSLA(sysCtx, "")
			return jobs.Result{RowsWritten: int64(len(raised))}, err
		}).
		Register("inventory.reservation_expiry", func(jobCtx context.Context) (jobs.Result, error) {
			n, err := graph.money.inventorySvc.ExpireDueReservations(jobCtx)
			return jobs.Result{RowsWritten: int64(n)}, err
		}).
		Register("yard.stale_pick_sweep", func(jobCtx context.Context) (jobs.Result, error) {
			scanned, notified, err := graph.ops.yardTasksSvc.SweepStalePicks(jobCtx, stalePickWindow, 0)
			return jobs.Result{
				RowsRead:    int64(scanned),
				RowsWritten: int64(notified),
				Detail:      map[string]any{"window": stalePickWindow.String()},
			}, err
		})
	go jobRunner.Run(ctx) // single instance, no leader election

	// The two W3-D jobs, registered on the SAME runner instance in the
	// pin's order (after Run, as the pin shipped it; registration is not
	// activation: both schedules are enabled = FALSE until a human says
	// so, and the ARNotWired arm reports SKIPPED in words).
	jobRunner.
		Register("ar.sms_collections", func(jobCtx context.Context) (jobs.Result, error) {
			res, err := graph.tops.arSweep.Run(jobCtx)
			if errors.Is(err, messagingprovider.ErrARNotWired) {
				// Not a failure - an unwired automation reports SKIPPED so
				// an operator reading job_runs sees "this did nothing on
				// purpose" rather than a red row they learn to ignore.
				return jobs.Result{Skipped: true, Detail: map[string]any{"reason": err.Error()}}, nil
			}
			return jobs.Result{
				RowsRead:    int64(res.Considered),
				RowsWritten: int64(res.Enqueued),
				Detail: map[string]any{
					"enqueued": res.Enqueued, "suppressed": res.Suppressed, "already_decided": res.Skipped,
				},
			}, err
		}).
		Register("quote.followup_cadence", func(jobCtx context.Context) (jobs.Result, error) {
			res, err := graph.money.quoteAutomation.SweepFollowUps(jobCtx)
			if errors.Is(err, quote.ErrFollowUpNotWired) || errors.Is(err, quote.ErrNoticeQueueUnwired) {
				return jobs.Result{Skipped: true, Detail: map[string]any{"reason": err.Error()}}, nil
			}
			return jobs.Result{
				RowsRead:    int64(res.PlansConsidered),
				RowsWritten: int64(res.Enqueued),
				Detail: map[string]any{
					"enqueued": res.Enqueued, "skipped": res.Skipped, "cancelled": res.Cancelled,
				},
			}, err
		})

	fmt.Println("core: worker: loops started (events dispatcher, sms dispatcher, jobs runner, retention, reconciler)")
	<-ctx.Done()
	fmt.Println("core: worker: shutdown complete")
	return nil
}

// registerSmsConsumers registers the sms dispatcher's whole topic set over
// the shared belt: the worker's one sms wiring point, extracted from the
// role body so a test can drive the SAME registration set the worker
// builds rather than a hand copy that would keep passing while the real
// line drifted (the HH-188 finding).
func registerSmsConsumers(belt *smsBelt, mail notify.Notifier, d *notify.Dispatcher, log *slog.Logger) {
	// The Wave 2 registration line, the pin's own and untouched: the AR and
	// fulfilment notices.
	messagingprovider.NewConsumer(belt.notifier, belt.resolver, log).
		RegisterHandlers(d)
	// The Wave 3 line the pin carried (D37's unchanged-behaviour carry):
	// the quote share and follow-up topics, registered SECRET because their
	// payloads carry the share link, a bearer credential for the public
	// quote view and accept routes, with the portal's mail notifier as the
	// follow-up cadence's email transport (the dev log stub by default).
	// Losing this line dead-letters every quote share and follow-up notice
	// the producers enqueue, with the link still in the row.
	messagingprovider.NewConsumer(belt.notifier, belt.resolver, log).
		WithEmailNotifier(mail).
		RegisterQuoteHandlers(d)
}

// approvalSourceAdapter satisfies messaging's reconcile-source seam over
// the rail's ApprovalReconcile read: the pin's adapter, re-pointed at the
// bridge's Approvals (the sanctioned class; decide-time authority never
// runs through it).
type approvalSourceAdapter struct{ svc *identity.Approvals }

func (a approvalSourceAdapter) ApprovalReconcile(ctx context.Context) (messaging.ReconcilePlan, error) {
	pending, decided, err := a.svc.ApprovalReconcile(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		return messaging.ReconcilePlan{}, err
	}
	plan := messaging.ReconcilePlan{}
	for _, p := range pending {
		plan.Pending = append(plan.Pending, messaging.PendingApproval{
			Ref: messaging.ApprovalRef{
				ApprovalID:  p.ApprovalID,
				PolicyType:  string(p.Policy),
				SubjectRef:  p.SubjectRef,
				BranchID:    p.BranchID,
				AmountCents: p.AmountCents,
				RequestedBy: p.RequestedBy,
				Detail:      p.Detail,
				DeepLink:    p.DeepLink,
				Status:      "PENDING",
			},
			ApproverIDs: p.ApproverIDs,
		})
	}
	for _, d := range decided {
		plan.Decided = append(plan.Decided, messaging.DecidedApproval{
			ApprovalID:  d.ApprovalID,
			Status:      d.Status,
			DecidedBy:   d.DecidedBy,
			RequestedBy: d.RequestedBy,
		})
	}
	return plan, nil
}
