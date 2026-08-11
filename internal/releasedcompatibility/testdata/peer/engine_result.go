package main

type EngineResult struct {
	Protocol             string `json:"protocol"`
	WrongTokenRejected   bool   `json:"wrong_token_rejected"`
	CompletedText        string `json:"completed_text"`
	CancellationTerminal string `json:"cancellation_terminal"`
	ActiveRuns           uint64 `json:"active_runs"`
}
