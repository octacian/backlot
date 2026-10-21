package v1

// PrivateConfig holds only project-specific input and executable overrides.
type PrivateConfig struct {
	Version string            `json:"version" yaml:"version"`
	Inputs  map[string]string `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Tools   map[string]string `json:"tools,omitempty" yaml:"tools,omitempty"`
}

// ProjectSetupRequest describes a local atomic edit; values come from a document or terminal.
// RevealSecrets is inspection-only and must never enter daemon contracts.
type ProjectSetupRequest struct {
	ProjectPath   string         `json:"project_path,omitempty"`
	ConfigPath    string         `json:"config_path,omitempty"`
	Update        *PrivateConfig `json:"update,omitempty"`
	Generate      []string       `json:"generate,omitempty"`
	RemoveInputs  []string       `json:"remove_inputs,omitempty"`
	RemoveTools   []string       `json:"remove_tools,omitempty"`
	Edit          bool           `json:"edit,omitempty"`
	Reassociate   bool           `json:"reassociate,omitempty"`
	RevealSecrets bool           `json:"reveal_secrets,omitempty"`
}

// SetupInput reports declaration status without ordinary value disclosure.
type SetupInput struct {
	Name          string  `json:"name"`
	Description   string  `json:"description,omitempty"`
	Secret        bool    `json:"secret"`
	Generate      string  `json:"generate,omitempty"`
	Configured    bool    `json:"configured"`
	RevealedValue *string `json:"revealed_value,omitempty"`
}

// SetupTool reports executable syntax; setup never checks tool availability.
type SetupTool struct {
	Name       string `json:"name"`
	Executable string `json:"executable"`
	Overridden bool   `json:"overridden"`
}

// ProjectSetupResponse is the named local setup/inspection envelope.
type ProjectSetupResponse struct {
	APIVersion  string       `json:"api_version"`
	Registered  bool         `json:"registered"`
	Changed     bool         `json:"changed"`
	Association string       `json:"association,omitempty"`
	PrivatePath string       `json:"private_path,omitempty"`
	Inputs      []SetupInput `json:"inputs"`
	Tools       []SetupTool  `json:"tools"`
}
