package agent

// State represents the temporary execution state of a running ReAct loop.
type State struct {
	CurrentIteration int
	MaxIterations    int
	Finished         bool
	CurrentTool      string
	LastError        error
	Cancelled        bool
}
