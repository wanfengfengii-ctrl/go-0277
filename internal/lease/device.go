package lease

import "context"

// Outcome is the deterministic result of a scripted device invocation. A
// device may succeed, refuse, disconnect or time out; the application layer
// never fabricates a success or a receipt from a failure.
type Outcome string

const (
	OutcomeSuccess      Outcome = "SUCCESS"
	OutcomeRefused      Outcome = "REFUSED"
	OutcomeDisconnected Outcome = "DISCONNECTED"
	OutcomeTimeout      Outcome = "TIMEOUT"
)

// Adapter executes a device invocation outside the storage transaction. It is
// the single seam through which scripted sensors and fan circuits are injected
// in tests and used by the production entry point.
type Adapter interface {
	// Run attempts one device call. kind is one of the catalog DeviceKind
	// string values (DOSING_CAGE, FAN_CIRCUIT, SAMPLING_LINE).
	Run(ctx context.Context, deviceCode, kind string) (Outcome, error)
}

// ScriptedAdapter replays a fixed per-device script of outcomes and then
// repeats its final outcome. It is used by tests and by the smoke harness to
// exercise refusal, disconnection and timeout paths deterministically.
type ScriptedAdapter struct {
	Scripts map[string][]Outcome
}

// NewScriptedAdapter builds an adapter from a device-code -> outcome-script map.
func NewScriptedAdapter(scripts map[string][]Outcome) *ScriptedAdapter {
	return &ScriptedAdapter{Scripts: scripts}
}

// Run consumes the next scripted outcome for the device, or repeats the final
// one once the script is exhausted.
func (s *ScriptedAdapter) Run(_ context.Context, deviceCode, _ string) (Outcome, error) {
	script := s.Scripts[deviceCode]
	if len(script) == 0 {
		return OutcomeSuccess, nil
	}
	next := script[0]
	if len(script) > 1 {
		s.Scripts[deviceCode] = script[1:]
	}
	return next, nil
}

// SuccessAdapter always succeeds and is the production default when no
// scripted device behaviour is configured.
type SuccessAdapter struct{}

// Run always returns success.
func (SuccessAdapter) Run(_ context.Context, _, _ string) (Outcome, error) {
	return OutcomeSuccess, nil
}
