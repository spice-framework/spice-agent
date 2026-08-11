package main

type releasedGeneration struct {
	Role      string `json:"role"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	ModuleSum string `json:"module_sum"`
	GoModSum  string `json:"go_mod_sum"`
}
