package bistrack

// direction.go, DIB-53 phase 0 the per-phase, per-entity DIRECTION-OF-TRUTH, declared as typed DATA
// now (supervisor C5). NOTHING reads this table in phase 0; it freezes a shape so the W10 worker stays
// declarative, it consults this policy instead of scattering "who is master right now" across
// if-statements that drift.
//
// The migration runs mirror → shadow → flip, and the master differs by phase AND by entity (this is the
// A-18 recorded M9 policy, not speculation):
//
//	MIRROR , pre-cutover: BisTrack is master. One-way PULL builds the native mirror. No push.
//	SHADOW , the 60-day parallel run: staff work NATIVE; PUSH keeps BisTrack current; PULL catches
//	          net-new rows still entered in BisTrack. This is the window the "rollback is free until
//	          November" guarantee lives in, lose push here and the guarantee dies.
//	FLIP   , post-cutover: HardscapeOS is master, BisTrack read-only. Pull only for late stragglers;
//	          push stops.
//
// A `master` of MasterNone with no directions means "not synced in this phase for this entity", a
// deliberate SKIP, not an omission.

// MigrationPhase is where the org sits on the mirror → shadow → flip path.
type MigrationPhase string

const (
	PhaseMirror MigrationPhase = "mirror"
	PhaseShadow MigrationPhase = "shadow"
	PhaseFlip   MigrationPhase = "flip"
)

// Master is which system is the source of truth for an entity in a phase.
type Master string

const (
	MasterBisTrack Master = "bistrack"
	MasterNative   Master = "native"
	MasterNone     Master = "none"
)

// DirectionPolicy is the ruling for one (phase, entity): who is master and which sync directions are
// allowed. AllowPull/AllowPush are what a future scheduler consults before enqueuing a job.
type DirectionPolicy struct {
	Phase     MigrationPhase
	Entity    EntityType
	Master    Master
	AllowPull bool
	AllowPush bool
}

// directionOfTruth is the frozen M9 policy. All four entities move together through the phases here;
// per-entity divergence (e.g. an entity that flips earlier) is expressed by editing its rows, which is
// exactly why this is data and not code.
var directionOfTruth = func() map[MigrationPhase]map[EntityType]DirectionPolicy {
	out := map[MigrationPhase]map[EntityType]DirectionPolicy{}
	set := func(p MigrationPhase, m Master, pull, push bool) {
		out[p] = map[EntityType]DirectionPolicy{}
		for _, e := range AllEntities {
			out[p][e] = DirectionPolicy{Phase: p, Entity: e, Master: m, AllowPull: pull, AllowPush: push}
		}
	}
	set(PhaseMirror, MasterBisTrack, true, false) // BisTrack master: pull-only mirror
	set(PhaseShadow, MasterNative, true, true)    // parallel run: native master, push keeps BisTrack current, pull net-new
	set(PhaseFlip, MasterNative, true, false)     // native master: BisTrack read-only, pull stragglers only
	return out
}()

// DirectionFor returns the ruling for one (phase, entity). ok=false for an unknown phase/entity.
func DirectionFor(phase MigrationPhase, entity EntityType) (DirectionPolicy, bool) {
	byEntity, ok := directionOfTruth[phase]
	if !ok {
		return DirectionPolicy{}, false
	}
	p, ok := byEntity[entity]
	return p, ok
}
