package observe

// Kind names what an event records. Every emit site in the app declares the
// kind of its event, so forensics reads one stable field instead of
// substring-matching messages. Kinds are persisted in the run's JSONL event
// log, the durable record, and carried on the event record the debug
// commands serialize.
type Kind string

// components maps each kind to the event component its emit site used, so
// records written before kinds stay consistent with records written after.
// declareKind is the only way to name a kind, so a kind cannot exist without
// a component.
var components = map[Kind]string{}

func declareKind(name, component string) Kind {
	kind := Kind(name)
	components[kind] = component
	return kind
}

var (
	KindRunStarted        = declareKind("run_started", "run")
	KindRunFinished       = declareKind("run_finished", "run")
	KindAuthState         = declareKind("auth_state", "provider")
	KindReleasesFetched   = declareKind("releases_fetched", "provider")
	KindClassifyMatched   = declareKind("classify_matched", "classify")
	KindClassifyUnmatched = declareKind("classify_unmatched", "classify")
	KindReleaseUnchanged  = declareKind("release_unchanged", "sync")
	KindReleasePlanned    = declareKind("release_planned", "sync")
	KindReleaseSynced     = declareKind("release_synced", "sync")
	KindPublishPlanned    = declareKind("publish_planned", "publish")
	KindPublishSkipped    = declareKind("publish_skipped", "publish")
	KindPublishHeld       = declareKind("publish_held", "publish")
	KindPublishCompleted  = declareKind("publish_completed", "publish")
	KindPublishFailed     = declareKind("publish_failed", "publish")
)

// Component returns the event component for the kind. An undeclared kind can
// only come from a run log written by another build, so it reports the run
// itself rather than inventing a component.
func (k Kind) Component() string {
	if component, ok := components[k]; ok {
		return component
	}
	return "run"
}
