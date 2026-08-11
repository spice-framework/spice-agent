package main

type releasedGenerationEvidence struct {
	Workflow string `json:"workflow"`
	Run      uint64 `json:"run"`
	Commit   string `json:"commit"`
}
