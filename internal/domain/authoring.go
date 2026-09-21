package domain

type SelectorMatch struct {
	ID         string `json:"id,omitempty"`
	Kind       string `json:"kind"`
	Value      string `json:"value,omitempty"`
	Attachment string `json:"attachment,omitempty"`
}

type GuardResult struct {
	Field  string `json:"field"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type RuleAttempt struct {
	Reason    string          `json:"reason,omitempty"`
	Input     string          `json:"input"`
	Series    string          `json:"series"`
	Selectors []SelectorMatch `json:"selectors,omitempty"`
	Guards    []GuardResult   `json:"guards,omitempty"`
	Matched   bool            `json:"matched"`
	Selected  bool            `json:"selected"`
}

type DecisionExplanation struct {
	Conflicts   []DecisionConflict `json:"conflicts,omitempty"`
	HeldReasons []string           `json:"held_reasons,omitempty"`
	Attempts    []RuleAttempt      `json:"attempts"`
	Overlaps    []string           `json:"overlaps,omitempty"`
}

type ContentReference struct {
	AttachmentIndex int    `json:"attachment_index,omitempty"`
	Kind            string `json:"kind"`
	FileName        string `json:"file_name,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
}

type DecisionConflict struct {
	LabelSeries string `json:"label_series"`
	TitleSeries string `json:"title_series"`
	LabelInput  string `json:"label_input"`
	TitleInput  string `json:"title_input"`
}
