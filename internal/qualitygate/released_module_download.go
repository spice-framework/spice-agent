package main

type releasedModuleDownload struct {
	Path       string   `json:"Path"`
	Version    string   `json:"Version"`
	Query      string   `json:"Query"`
	Info       string   `json:"Info"`
	GoMod      string   `json:"GoMod"`
	Zip        string   `json:"Zip"`
	Dir        string   `json:"Dir"`
	Sum        string   `json:"Sum"`
	GoModSum   string   `json:"GoModSum"`
	Retracted  []string `json:"Retracted"`
	Deprecated string   `json:"Deprecated"`
	Error      string   `json:"Error"`
	Reuse      bool     `json:"Reuse"`
	Origin     struct {
		VCS       string `json:"VCS"`
		URL       string `json:"URL"`
		Subdir    string `json:"Subdir"`
		Hash      string `json:"Hash"`
		Ref       string `json:"Ref"`
		TagPrefix string `json:"TagPrefix"`
	} `json:"Origin"`
}
