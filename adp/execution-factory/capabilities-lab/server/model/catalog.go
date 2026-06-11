package model

type CatalogEntry struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	UpdateTime  int64  `json:"update_time"`
	Installed   bool   `json:"installed"`
	Version     string `json:"version,omitempty"`
}

type CatalogListResponse struct {
	Data     []CatalogEntry `json:"data"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
}

type InstallCatalogRequest struct {
	Kind     string `json:"kind" binding:"required"`
	SourceID string `json:"source_id" binding:"required"`
	Mode     string `json:"mode"`
	Name     string `json:"name,omitempty"`
}

type InstallCatalogResponse struct {
	ComponentType string       `json:"component_type"`
	Mode          string       `json:"mode"`
	Capabilities  []Capability `json:"capabilities"`
}
