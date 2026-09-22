package dispatch

// manifest.go, the printed run manifest: the sheet that rides in the cab.
//
// WHY SERVER-RENDERED, SELF-CONTAINED HTML. The same reasoning printing/render.go gives for the
// counter receipt, and it applies harder here: the yard has a browser and a print dialog, not a
// driver, a spooler or a PDF toolchain. One page with no external CSS, no fonts and no scripts prints
// the same bytes today and when it is reprinted from a dispute next March, and it needs no build step
// to change.
//
// html/template (NOT text/template) is the escaping boundary. Every value on this page, a customer's
// name, a site address, a dispatcher's note typed at 6am, is user-supplied text landing inside
// markup.
//
// THE DOCUMENT TYPE. This enqueues DELIVERY_DOC, the print_queue type 0088 defined for exactly this
// ("the driver's delivery paperwork") and which had never had a producer. module.go's PrintEnqueuer
// comment says why that is extension rather than reuse-by-accident.

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"strings"
)

// Manifest is the render model: a run, its stops in order, the branch it left from, and the honest
// distance estimate (absent when it cannot be computed).
type Manifest struct {
	Run         Run
	BranchName  string
	Stops       []Stop
	EstimatedKm *float64
	Ungeocoded  int
	PrintedAt   string

	// - W3-C / DIB-63: made-to-order compliance -
	//
	// MTOStops is the SAME stop list as Stops, decorated with each stop's classification, note and
	// weight. Both are carried because Stops is the shape every Wave 2 consumer (and its tests) pins;
	// the printed sheet reads MTOStops. They are built from one query in the same order, so the two
	// can never disagree about which stops are on the run.
	MTOStops []ManifestStop
	// MTO is the run-level roll-up (service_w3.go rollUpMTO). Computed per render, stored nowhere.
	MTO MTOSummary
}

// Manifest builds the printable model for a run (fenced), for both the HTML render and the print
// enqueue.
func (s *Service) Manifest(ctx context.Context, runID string) (Manifest, error) {
	run, err := s.guardRun(ctx, runID)
	if err != nil {
		return Manifest{}, err
	}
	d, err := s.runDetail(ctx, run)
	if err != nil {
		return Manifest{}, err
	}
	branch, err := s.repo.BranchName(ctx, run.BranchID)
	if err != nil {
		return Manifest{}, err
	}
	// DIB-63: the compliance decoration. One query for every stop's classification, plus the truck's
	// advisory capacity, rolled up by the pure function in service_w3.go.
	mtoStops, summary, err := s.manifestMTO(ctx, d.Run, d.Stops)
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{
		Run: d.Run, BranchName: branch, Stops: d.Stops,
		EstimatedKm: d.EstimatedKm, Ungeocoded: d.UngeocodedStops,
		PrintedAt: s.now().UTC().Format("2006-01-02 15:04 UTC"),
		MTOStops:  mtoStops, MTO: summary,
	}, nil
}

// PrintManifest records that the manifest was printed, through the shared print_queue.
//
// The queue row is the point: "was a manifest printed for this run, when, by whom, how many times" is
// a question a dispatcher asks on the morning a driver arrives without paperwork, and before this
// there was nowhere for the answer to live. Enqueue → the caller renders and prints → the row stands
// as the record.
func (s *Service) PrintManifest(ctx context.Context, runID string) (string, error) {
	run, err := s.guardRun(ctx, runID)
	if err != nil {
		return "", err
	}
	if s.prints == nil {
		return "", fmt.Errorf("dispatch: print queue is not wired")
	}
	jobID, err := s.prints.Enqueue(ctx, PrintJobRequest{
		DocType:      "DELIVERY_DOC",
		RefID:        run.ID,
		BranchID:     run.BranchID,
		RenderTarget: "BROWSER",
	})
	if err != nil {
		return "", err
	}
	return jobID, nil
}

// RenderManifestHTML renders the manifest as a self-contained print document.
//
// W3-C: a Manifest built the Wave 2 way, Stops populated, MTOStops empty, still renders every stop,
// classified STANDARD. That is not politeness toward old callers; it is the guarantee that adding a
// compliance column cannot make a manifest print BLANK for anyone who assembles the model without it.
// A missing classification renders as stock material, which is what an unclassified load is.
func RenderManifestHTML(m Manifest) (string, error) {
	if len(m.MTOStops) == 0 && len(m.Stops) > 0 {
		m.MTOStops = make([]ManifestStop, 0, len(m.Stops))
		for _, st := range m.Stops {
			m.MTOStops = append(m.MTOStops, ManifestStop{Stop: st, MTO: StopMTO{Class: MTOStandard}})
		}
		m.MTO = rollUpMTO(m.MTOStops, nil)
	}
	var buf bytes.Buffer
	if err := manifestTmpl.Execute(&buf, m); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// stopAddress joins the address parts that are present. A blank line in a printed address is how a
// driver ends up at the wrong side of a concession road.
func stopAddress(s Stop) string {
	parts := []string{}
	for _, p := range []string{s.AddressLine, s.City, s.PostalCode} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, strings.TrimSpace(p))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// km formats the estimate to one decimal. One decimal, not three: the number is a straight-line
// approximation and printing 34.271 km would claim a precision the method does not have.
func km(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.1f km", *v)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

var manifestTmpl = template.Must(template.New("manifest").Funcs(template.FuncMap{
	"address":  stopAddress,
	"km":       km,
	"orDash":   orDash,
	"mtoLabel": mtoClassLabel,
	"weight":   weightLabel,
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Run manifest {{.Run.RunNo}}</title>
<style>
  /* Deliberately plain and self-contained: this page has to print identically from any browser on
     any machine in the yard, including one that has never loaded this app before. */
  @page { size: letter; margin: 14mm; }
  body { font: 12px/1.45 -apple-system, "Segoe UI", system-ui, sans-serif; color: #111; margin: 0; }
  h1 { font-size: 18px; margin: 0 0 2px; }
  .sub { color: #555; margin-bottom: 14px; }
  .crew { display: flex; gap: 26px; margin: 0 0 16px; padding: 10px 12px; border: 1px solid #bbb; }
  .crew div { font-size: 12px; }
  .crew b { display: block; font-size: 10px; text-transform: uppercase; letter-spacing: .06em; color: #666; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; vertical-align: top; padding: 7px 6px; border-bottom: 1px solid #ddd; }
  th { font-size: 10px; text-transform: uppercase; letter-spacing: .06em; color: #555; border-bottom: 1.5px solid #888; }
  td.seq { width: 26px; font-weight: 700; }
  .note { color: #444; font-style: italic; }
  .sign { margin-top: 4px; padding-top: 3px; border-top: 1px dashed #999; color: #777; font-size: 10px; }
  .foot { margin-top: 18px; font-size: 10px; color: #666; }
  .warn { margin-top: 10px; padding: 7px 9px; border: 1px solid #999; font-size: 11px; }
  /* DIB-63: the compliance band. Deliberately a BOX WITH A BORDER and not a colour, this sheet is
     printed on whatever mono laser is in the yard, and a rule expressed only in colour is a rule that
     does not survive the print. */
  .mto { margin: 0 0 14px; padding: 8px 10px; border: 1.5px solid #333; }
  .mto b { display: block; font-size: 10px; text-transform: uppercase; letter-spacing: .06em; color: #444; }
  .mto .cls { font-size: 14px; font-weight: 700; letter-spacing: .02em; }
  .mto .parts { display: flex; gap: 24px; margin-top: 5px; font-size: 11px; }
  /* Only the CLASS is upper-cased. The weight and the handling note are not: "CUT TO 1150MM FOR THIS
     DRIVEWAY" reads as shouting on a sheet a driver is already scanning, and a unit in caps ("3100 KG")
     is a unit somebody has to re-read. */
  td.mtocell { font-size: 11px; }
  td.mtocell .clsname { text-transform: uppercase; letter-spacing: .04em; font-size: 10px; }
  td.mtocell .made { font-weight: 700; }
  @media print { .noprint { display: none; } }
</style>
</head>
<body>
<h1>Run manifest {{.Run.RunNo}}</h1>
<div class="sub">{{.BranchName}} · {{.Run.ScheduledDate}} · {{.Run.Status}}</div>

<div class="crew">
  <div><b>Truck</b>{{orDash .Run.TruckCode}}</div>
  <div><b>Driver</b>{{orDash .Run.DriverName}}</div>
  <div><b>Stops</b>{{len .Stops}}</div>
  {{if .EstimatedKm}}<div><b>Straight-line est.</b>{{km .EstimatedKm}}</div>{{end}}
</div>

<!-- DIB-63 · MTO compliance band. The load's classification is stated ONCE at the top, in words, and
     then per stop in the table, because the handling rule ("this cannot be swapped, this cannot be
     tipped, this comes back to us if it is refused") is a decision the driver makes at the gate, not
     one they should have to derive by reading twelve rows. The weight total names how many stops were
     actually weighed: a partial total compared against a truck's capacity is a number that means
     nothing, and the sheet must not let anybody read it as the weight of the load. -->
<div class="mto">
  <b>Load classification</b>
  <div class="cls">{{mtoLabel .MTO.Class}}</div>
  <div class="parts">
    <div>Made-to-order stops: <strong>{{.MTO.MTOStops}}</strong> of {{len .MTOStops}}</div>
    <div>Weighed: <strong>{{.MTO.TotalWeightKg}} kg</strong> across {{.MTO.WeighedStops}} stop(s){{if .MTO.UnweighedStops}} · {{.MTO.UnweighedStops}} not weighed{{end}}</div>
    {{if .MTO.CapacityKg}}<div>Truck capacity: {{.MTO.CapacityKg}} kg</div>{{end}}
  </div>
  {{if .MTO.OverCapacity}}<div class="warn">This load's recorded weight exceeds the truck's capacity. Capacity is advisory in this system, check it before you leave.</div>{{end}}
</div>

{{if .Run.Notes}}<p class="note">{{.Run.Notes}}</p>{{end}}

<table>
  <thead>
    <tr><th class="seq">#</th><th>Order</th><th>Customer &amp; address</th><th>Class / weight</th><th>Window</th><th>Contact</th><th>Received by</th></tr>
  </thead>
  <tbody>
  {{range .MTOStops}}
    <tr>
      <td class="seq">{{.StopNo}}</td>
      <td>{{orDash .OrderNo}}<br><span class="note">{{.Status}}</span></td>
      <td>{{orDash .CustomerName}}<br>{{address .Stop}}{{if .Instructions}}<br><span class="note">{{.Instructions}}</span>{{end}}</td>
      <td class="mtocell"><span class="clsname {{if ne .MTO.Class "STANDARD"}}made{{end}}">{{mtoLabel .MTO.Class}}</span><br>{{weight .MTO.WeightKg}}{{if .MTO.Note}}<br><span class="note">{{.MTO.Note}}</span>{{end}}</td>
      <td>{{orDash .TimeWindow}}</td>
      <td>{{orDash .ContactName}}{{if .ContactPhone}}<br>{{.ContactPhone}}{{end}}</td>
      <!-- The paper line is a FALLBACK, not the record. The signed-for name that counts is the typed
           attestation captured in the app (0090 pod_signature_ref); this line exists for the run where
           the phone is dead, and it is transcribed into the app afterwards. Saying so on the sheet
           stops it from quietly becoming the primary evidence. -->
      <td>{{if .PODReceivedBy}}{{.PODReceivedBy}}{{else}}<div class="sign">sign &amp; print name</div>{{end}}</td>
    </tr>
  {{else}}
    <tr><td colspan="7">No stops on this run.</td></tr>
  {{end}}
  </tbody>
</table>

{{if .Ungeocoded}}
<div class="warn">{{.Ungeocoded}} stop(s) on this run have no map coordinates, so they are not in the
distance estimate. The estimate is straight-line distance from the yard and back, not driving
distance, expect the road to be materially longer.</div>
{{end}}

<div class="foot">Printed {{.PrintedAt}} · {{.Run.RunNo}} · proof of delivery is captured in HH Yard,
this sheet is the driver's working copy.</div>
</body>
</html>
`))
