package model

import (
	"context"
	"database/sql"
	"time"
)

// FunctionMetadataDB function metadata database.
type FunctionMetadataDB struct {
	ID              int64  `json:"f_id" db:"f_id"`
	Summary         string `json:"f_summary" db:"f_summary"`
	Version         string `json:"f_version" db:"f_version"`
	Description     string `json:"f_description" db:"f_description"`
	Path            string `json:"f_path" db:"f_path"`
	ServerURL       string `json:"f_svc_url" db:"f_svc_url"`
	Method          string `json:"f_method" db:"f_method"`
	APISpec         string `json:"f_api_spec" db:"f_api_spec"`
	CreateUser      string `json:"f_create_user" db:"f_create_user"`
	CreateTime      int64  `json:"f_create_time" db:"f_create_time"`
	UpdateUser      string `json:"f_update_user" db:"f_update_user"`
	UpdateTime      int64  `json:"f_update_time" db:"f_update_time"`
	ScriptType      string `json:"f_script_type" db:"f_script_type"`   // Script type, such as Python, Node.js.
	Code            string `json:"f_code" db:"f_code"`                 // function code.
	Dependencies    string `json:"f_dependencies" db:"f_dependencies"` // Third-party package dependencies, such as the requests library in Python.
	DependenciesURL string `json:"f_dependencies_url" db:"f_dependencies_url"`
	ErrMessage      string `json:"-"` // error message.
}

// IFunctionMetadataDB function metadata database interface.
type IFunctionMetadataDB interface {
	InsertFuncMetadata(ctx context.Context, tx *sql.Tx, metadata *FunctionMetadataDB) (version string, err error)
	SelectByVersion(ctx context.Context, version string) (exist bool, metadata *FunctionMetadataDB, err error)
	UpdateByVersion(ctx context.Context, tx *sql.Tx, metadata *FunctionMetadataDB) error
	DeleteByVersion(ctx context.Context, tx *sql.Tx, version string) error
	DeleteByVersions(ctx context.Context, tx *sql.Tx, versions []string) error
	InsertFuncMetadatas(ctx context.Context, tx *sql.Tx, metadatas []*FunctionMetadataDB) (versions []string, err error)
	SelectListByVersion(ctx context.Context, versions []string) ([]*FunctionMetadataDB, error)
}

// GetType Gets the resource type.
func (f *FunctionMetadataDB) GetType() string {
	return string(SourceTypeFunction)
}

// GetSummary Get summary.
func (f *FunctionMetadataDB) GetSummary() string {
	return f.Summary
}

// GetDescription Get function description.
func (f *FunctionMetadataDB) GetDescription() string {
	if f.Description == "" {
		return f.Summary
	}
	return f.Description
}

// GetVersion Get version.
func (f *FunctionMetadataDB) GetVersion() string {
	return f.Version
}
func (f *FunctionMetadataDB) GetMethod() string {
	return f.Method
}
func (f *FunctionMetadataDB) GetPath() string {
	return f.Path
}

func (f *FunctionMetadataDB) GetScriptType() string {
	return f.ScriptType
}

func (f *FunctionMetadataDB) Validate(ctx context.Context) error {
	return nil
}

func (f *FunctionMetadataDB) SetSummary(summary string) {
	f.Summary = summary
}
func (f *FunctionMetadataDB) SetDescription(description string) {
	f.Description = description
}

func (f *FunctionMetadataDB) SetMethod(method string) {
	f.Method = method
}
func (f *FunctionMetadataDB) SetPath(path string) {
	f.Path = path
}
func (f *FunctionMetadataDB) SetVersion(version string) {
	f.Version = version
}
func (f *FunctionMetadataDB) SetScriptType(scriptType string) {
	f.ScriptType = scriptType
}

func (f *FunctionMetadataDB) GetServerURL() string {
	return f.ServerURL
}
func (f *FunctionMetadataDB) SetServerURL(serverURL string) {
	f.ServerURL = serverURL
}

// GetAPISpec Get API specification.
func (f *FunctionMetadataDB) GetAPISpec() string {
	return f.APISpec
}

func (f *FunctionMetadataDB) SetAPISpec(apiSpec string) {
	f.APISpec = apiSpec
}

// GetUpdateUser Gets the update user.
func (f *FunctionMetadataDB) GetUpdateUser() (user string) {
	return f.UpdateUser
}

func (f *FunctionMetadataDB) SetUpdateInfo(user string) {
	f.UpdateUser = user
	f.UpdateTime = time.Now().UnixNano()
}

// GetCreateUser Gets the created user.
func (f *FunctionMetadataDB) GetCreateUser() (user string) {
	return f.CreateUser
}

// SetCreateInfo sets creation information.
func (f *FunctionMetadataDB) SetCreateInfo(user string) {
	f.CreateUser = user
	f.CreateTime = time.Now().UnixNano()
}

func (f *FunctionMetadataDB) GetErrMessage() string {
	return f.ErrMessage
}

func (f *FunctionMetadataDB) GetCode() string {
	return f.Code
}
func (f *FunctionMetadataDB) SetCode(code string) {
	f.Code = code
}

func (f *FunctionMetadataDB) GetDependenciesURL() string {
	return f.DependenciesURL
}
func (f *FunctionMetadataDB) SetDependenciesURL(dependenciesURL string) {
	f.DependenciesURL = dependenciesURL
}
func (f *FunctionMetadataDB) SetDependencies(dependencies string) {
	f.Dependencies = dependencies
}
func (f *FunctionMetadataDB) GetDependencies() string {
	if f.Dependencies == "null" {
		return ""
	}
	return f.Dependencies
}
