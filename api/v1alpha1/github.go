package v1alpha1

// Repo defines an unique GitHub repo
type Repo struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

// ETag represents the value of ETag header received by the latest API call
type ETag string
