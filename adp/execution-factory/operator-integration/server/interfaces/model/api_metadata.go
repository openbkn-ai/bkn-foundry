// Package model defines database operation interface.
// @file api_metadata.go
// @description: Define t_metadata_api table operation interface.
package model

//go:generate mockgen -source=api_metadata.go -destination=../../mocks/model_api_metadata.go -package=mocks
import (
	"context"
	"database/sql"
	"time"
)

// APIMetadataDB API metadata database.
type APIMetadataDB struct {
	ID          int64  `json:"f_id" db:"f_id"`
	Summary     string `json:"f_summary" db:"f_summary"`
	Version     string `json:"f_version" db:"f_version"`
	Description string `json:"f_description" db:"f_description"`
	Path        string `json:"f_path" db:"f_path"`
	ServerURL   string `json:"f_svc_url" db:"f_svc_url"`
	Method      string `json:"f_method" db:"f_method"`
	APISpec     string `json:"f_api_spec" db:"f_api_spec"`
	CreateUser  string `json:"f_create_user" db:"f_create_user"`
	CreateTime  int64  `json:"f_create_time" db:"f_create_time"`
	UpdateUser  string `json:"f_update_user" db:"f_update_user"`
	UpdateTime  int64  `json:"f_update_time" db:"f_update_time"`
	ErrMessage  string `json:"-"` // error message.
}

// IAPIMetadataDB API metadata database.
type IAPIMetadataDB interface {
	InsertAPIMetadata(ctx context.Context, tx *sql.Tx, metadata *APIMetadataDB) (version string, err error)
	SelectByVersion(ctx context.Context, version string) (has bool, metadata *APIMetadataDB, err error)
	UpdateByVersion(ctx context.Context, tx *sql.Tx, version string, metadata *APIMetadataDB) error
	UpdateByID(ctx context.Context, tx *sql.Tx, id int64, metadata *APIMetadataDB) error
	DeleteByVersion(ctx context.Context, tx *sql.Tx, version string) error
	DeleteByVersions(ctx context.Context, tx *sql.Tx, versions []string) error
	InsertAPIMetadatas(ctx context.Context, tx *sql.Tx, metadatas []*APIMetadataDB) (versions []string, err error)
	SelectListByVersion(ctx context.Context, versions []string) ([]*APIMetadataDB, error)
}

// GetType Gets the resource type.
func (a *APIMetadataDB) GetType() string {
	return string(SourceTypeOpenAPI)
}

// GetSummary Get summary.
func (a *APIMetadataDB) GetSummary() string {
	return a.Summary
}

// GetDescription Get function description.
func (a *APIMetadataDB) GetDescription() string {
	if a.Description == "" {
		return a.Summary
	}
	return a.Description
}

// GetVersion Get version.
func (a *APIMetadataDB) GetVersion() string {
	return a.Version
}
func (a *APIMetadataDB) GetMethod() string {
	return a.Method
}
func (a *APIMetadataDB) GetPath() string {
	return a.Path
}
func (a *APIMetadataDB) GetScriptType() string {
	return ""
}

func (a *APIMetadataDB) Validate(ctx context.Context) error {
	return nil
}

func (a *APIMetadataDB) SetSummary(summary string) {
	a.Summary = summary
}
func (a *APIMetadataDB) SetDescription(description string) {
	a.Description = description
}

func (a *APIMetadataDB) SetMethod(method string) {
	a.Method = method
}
func (a *APIMetadataDB) SetPath(path string) {
	a.Path = path
}
func (a *APIMetadataDB) SetVersion(version string) {
	a.Version = version
}
func (a *APIMetadataDB) SetScriptType(scriptType string) {
	// Setting the runtime is not supported.
}
func (a *APIMetadataDB) GetServerURL() string {
	return a.ServerURL
}
func (a *APIMetadataDB) SetServerURL(serverURL string) {
	a.ServerURL = serverURL
}

// GetAPISpec Get API specification.
func (a *APIMetadataDB) GetAPISpec() string {
	return a.APISpec
}

func (a *APIMetadataDB) SetAPISpec(apiSpec string) {
	a.APISpec = apiSpec
}

// GetUpdateUser Gets the update user.
func (a *APIMetadataDB) GetUpdateUser() (user string) {
	return a.UpdateUser
}

func (a *APIMetadataDB) SetUpdateInfo(user string) {
	a.UpdateUser = user
	a.UpdateTime = time.Now().UnixNano()
}

// GetCreateUser Gets the created user.
func (a *APIMetadataDB) GetCreateUser() (user string) {
	return a.CreateUser
}

// SetCreateInfo sets creation information.
func (a *APIMetadataDB) SetCreateInfo(user string) {
	a.CreateUser = user
	a.CreateTime = time.Now().UnixNano()
}

func (a *APIMetadataDB) GetErrMessage() string {
	return a.ErrMessage
}

// func (a *APIMetadataDB) GetFunctionContent() (code, scriptType, dependencies string) {
// // Does not support getting function content.
// 	return code, scriptType, dependencies
// }
// func (a *APIMetadataDB) SetFunctionContent(code, scriptType, dependencies string) {
// // Setting function content is not supported.
// }

func (a *APIMetadataDB) GetCode() string {
	// Getting function content is not supported.
	return ""
}
func (a *APIMetadataDB) SetCode(code string) {
	// Setting function content is not supported.
}
func (a *APIMetadataDB) GetDependenciesURL() string {
	// Getting function content is not supported.
	return ""
}
func (a *APIMetadataDB) SetDependenciesURL(dependenciesURL string) {
	// Setting function dependency URL is not supported.
}
func (a *APIMetadataDB) SetDependencies(dependencies string) {
	// Setting functional dependencies is not supported.
}
func (a *APIMetadataDB) GetDependencies() string {
	// Obtaining functional dependencies is not supported.
	return ""
}
