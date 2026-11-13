// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package project

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Paths of a project package; see interfaces.PackageManifest.
const (
	packageManifest      = "manifest.json"
	packageDocuments     = "requirements/documents.json"
	packageSpecification = "requirements/specification.json"
	packageDraft         = "model/draft.json"
	packageConfirmed     = "model/confirmed/"
	packageEditions      = "editions/"
	// packageMaxFiles bounds the entries a package may hold, so a crafted one cannot exhaust memory.
	packageMaxFiles = 10000
)

// packageDocument is a requirement document as a package carries it: the text extracted from the
// file and its blocks, as stored; the uploader and times are the importer's.
type packageDocument struct {
	ID      string                        `json:"id"`
	Name    string                        `json:"name"`
	DocType string                        `json:"doc_type"`
	Format  string                        `json:"format"`
	Content string                        `json:"content"`
	Blocks  []interfaces.RequirementBlock `json:"blocks"`
}

// packageSpecification is the requirement specification as a package carries it.
type packageSpec struct {
	Version int                           `json:"version"`
	Spec    interfaces.RequirementSpec    `json:"spec"`
	Inputs  *interfaces.RequirementInputs `json:"inputs,omitempty"`
}

// ExportProject packs the project: its manifest, requirement documents, specification, draft, the
// files its last confirmation wrote, and what the edition adds.
func (s *projectService) ExportProject(ctx context.Context, token string, id string,
	caller interfaces.AccountInfo) ([]byte, string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ExportProject")
	defer span.End()

	project, err := s.visibleProject(ctx, token, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, "", err
	}
	entry := &interfaces.ProjectEntry{Project: project}
	if s.pc != nil {
		s.ResolveCatalogs(ctx, token, entry, caller)
	}
	language := project.NamingLanguage
	if language == "" {
		language = interfaces.NAMING_LANGUAGE_ZH_CN
	}
	manifest := &interfaces.PackageManifest{
		Format: interfaces.PROJECT_PACKAGE_FORMAT, FormatVersion: interfaces.PROJECT_PACKAGE_VERSION,
		ExportTime: time.Now().UnixMilli(), Name: project.Name, NamingLanguage: language,
		KN: interfaces.PackageNetwork{ID: project.KNID}, Catalogs: []interfaces.PackageCatalog{},
		Tables: []interfaces.PackageTable{}, Contents: interfaces.PackageContents{Editions: []string{}},
	}
	if kn, err := s.bba.GetKN(ctx, token, project.KNID); err == nil && kn != nil {
		manifest.KN.Name = kn.Name
	}
	names, err := s.va.CatalogNames(ctx, caller, entry.CatalogIDs)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Name the catalogs of project %s failed: %v", id, err))
	}
	for _, catalogID := range entry.CatalogIDs {
		manifest.Catalogs = append(manifest.Catalogs, interfaces.PackageCatalog{ID: catalogID, Name: names[catalogID]})
	}

	files := map[string][]byte{}
	put := func(name string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		files[name] = b
		return err
	}
	docs, err := s.rqa.GetDocuments(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Read documents failed")
		return nil, "", internalError(ctx, "Read requirement documents failed", err)
	}
	if len(docs) > 0 {
		out := make([]packageDocument, 0, len(docs))
		for _, d := range docs {
			out = append(out, packageDocument{ID: d.ID, Name: d.Name, DocType: d.DocType, Format: d.Format, Content: d.Content, Blocks: d.Blocks})
		}
		if err = put(packageDocuments, out); err != nil {
			return nil, "", internalError(ctx, "Pack requirement documents failed", err)
		}
		manifest.Contents.Documents = len(out)
	}
	if req, found, err := s.rqa.GetRequirement(ctx, id); err != nil {
		span.SetStatus(codes.Error, "Read requirement failed")
		return nil, "", internalError(ctx, "Read requirement failed", err)
	} else if found && req.Status == interfaces.REQUIREMENT_STATUS_READY {
		if err = put(packageSpecification, packageSpec{Version: req.Version, Spec: req.Spec, Inputs: req.Inputs}); err != nil {
			return nil, "", internalError(ctx, "Pack requirement failed", err)
		}
		manifest.Contents.Requirement = true
	}
	draft, found, err := s.da.GetDraft(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Read draft failed")
		return nil, "", internalError(ctx, "Read draft failed", err)
	}
	if found && draft.Status == interfaces.DRAFT_STATUS_READY {
		if err = put(packageDraft, draft.Items); err != nil {
			return nil, "", internalError(ctx, "Pack draft failed", err)
		}
		manifest.Contents.Draft = true
		manifest.Tables = s.draftTables(ctx, caller, &draft.Items)
		if c := draft.Items.Confirmation; c != nil && c.Commit != "" && len(c.Files) > 0 && s.gs != nil {
			tree, err := s.gs.ReadTree(ctx, project.KNID, c.Commit)
			if err != nil {
				otellog.LogWarn(ctx, fmt.Sprintf("Read the confirmed model of project %s failed: %v", id, err))
			}
			for _, p := range c.Files {
				if b, ok := tree[p]; ok {
					files[packageConfirmed+p] = b
					manifest.Contents.ConfirmedFiles++
				}
			}
		}
	}
	if packager, ok := projecthooks.Current().Projects.(projecthooks.ProjectPackager); ok {
		parts, err := packager.ExportParts(ctx, token, id, caller)
		if err != nil {
			span.SetStatus(codes.Error, "Export edition parts failed")
			return nil, "", err
		}
		for part, partFiles := range parts {
			if len(partFiles) == 0 || !validPartName(part) {
				continue
			}
			manifest.Contents.Editions = append(manifest.Contents.Editions, part)
			for p, b := range partFiles {
				files[packageEditions+part+"/"+p] = b
			}
		}
		sort.Strings(manifest.Contents.Editions)
	}

	pkg, err := writePackage(manifest, files)
	if err != nil {
		span.SetStatus(codes.Error, "Write package failed")
		return nil, "", internalError(ctx, "Write project package failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return pkg, packageFileName(project.Name), nil
}

// draftTables names the tables the draft reads, so an import can find them by name.
func (s *projectService) draftTables(ctx context.Context, caller interfaces.AccountInfo,
	items *interfaces.DraftItems) []interfaces.PackageTable {
	ids := []string{}
	if items.Inputs != nil {
		for _, t := range items.Inputs.Tables {
			ids = append(ids, t.ResourceID)
		}
	}
	for _, t := range items.Unreadable {
		ids = append(ids, t.ResourceID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	out := []interfaces.PackageTable{}
	if len(ids) == 0 {
		return out
	}
	tables, err := s.va.GetTables(ctx, caller, ids)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Name the tables of a draft failed: %v", err))
	}
	for _, t := range tables {
		out = append(out, interfaces.PackageTable{ID: t.ID, CatalogID: t.CatalogID, Name: t.Name})
	}
	for _, t := range items.Unreadable {
		if !slices.ContainsFunc(out, func(p interfaces.PackageTable) bool { return p.ID == t.ResourceID }) {
			out = append(out, interfaces.PackageTable{ID: t.ResourceID, CatalogID: t.CatalogID, Name: t.Name})
		}
	}
	return out
}

func (s *projectService) PreviewPackage(ctx context.Context, pkg []byte) (*interfaces.ProjectPackagePreview, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "PreviewProjectPackage")
	defer span.End()

	manifest, _, err := readPackage(ctx, pkg)
	if err != nil {
		span.SetStatus(codes.Error, "Read package failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return &interfaces.ProjectPackagePreview{
		FormatVersion: manifest.FormatVersion, Name: manifest.Name, NamingLanguage: manifest.NamingLanguage,
		KN: manifest.KN, Catalogs: manifest.Catalogs, Contents: manifest.Contents,
	}, nil
}

// ImportPackage creates the project as POST /projects does, in the package's language and on the
// mapped catalogs, then restores its material. A failure after creating it deletes it again.
func (s *projectService) ImportPackage(ctx context.Context, token string, pkg []byte, options interfaces.ImportProjectOptions,
	owner interfaces.AccountInfo, author interfaces.GitAuthor) (*interfaces.ProjectEntry, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ImportProjectPackage")
	defer span.End()

	manifest, files, err := readPackage(ctx, pkg)
	if err != nil {
		span.SetStatus(codes.Error, "Read package failed")
		return nil, err
	}
	// Everything the package says is read before anything is created: what can fail after it is
	// only a write, never the package, so a refused package leaves no network or project behind.
	material, err := readMaterial(ctx, files)
	if err != nil {
		span.SetStatus(codes.Error, "Read package failed")
		return nil, err
	}
	catalogIDs, unmapped := []string{}, []string{}
	for _, c := range manifest.Catalogs {
		target := strings.TrimSpace(options.CatalogMapping[c.ID])
		if target == "" {
			unmapped = append(unmapped, c.ID)
		} else if !slices.Contains(catalogIDs, target) {
			catalogIDs = append(catalogIDs, target)
		}
	}
	if len(unmapped) > 0 {
		span.SetStatus(codes.Error, "Catalog not mapped")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidCatalogMapping).
			WithErrorDetails(unmapped)
	}
	if len(catalogIDs) > 0 {
		missing, err := s.va.MissingCatalogs(ctx, owner, catalogIDs)
		if err != nil {
			span.SetStatus(codes.Error, "Check catalogs failed")
			return nil, internalError(ctx, "Check catalogs failed", err)
		}
		if len(missing) > 0 {
			span.SetStatus(codes.Error, "Catalog missing")
			return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Project_CatalogMissing).
				WithErrorDetails(missing)
		}
	}
	name := options.Name
	if strings.TrimSpace(name) == "" {
		name = manifest.Name
	}
	entry, err := s.CreateProject(ctx, token, interfaces.CreateProjectRequest{
		Name: name, KNID: options.KNID, NewKN: options.NewKN, CatalogIDs: catalogIDs,
		NamingLanguage: manifest.NamingLanguage, EnableVersioning: options.EnableVersioning,
	}, owner, author)
	if err != nil {
		span.SetStatus(codes.Error, "Create project failed")
		return nil, err
	}
	if err = s.restore(ctx, token, entry.Project, manifest, material, files, options.CatalogMapping, owner); err != nil {
		span.SetStatus(codes.Error, "Restore project failed")
		// Not left half imported: the project goes, as deleting it would take it.
		if derr := s.pa.DeleteProject(context.WithoutCancel(ctx), entry.ID); derr != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("Remove project %s after a failed import failed: %v", entry.ID, derr))
		} else if s.gs != nil {
			_ = s.gs.DeleteBranch(context.WithoutCancel(ctx), entry.KNID, entry.Branch)
		}
		return nil, err
	}
	// On an existing network, the catalogs it reads follow it and are never stored: only those the
	// package adds on top stay the project's, as when the owner saves the catalogs.
	if options.KNID != "" && s.pc != nil {
		network, err := s.pc.NetworkCatalogs(ctx, token, owner, entry.Project)
		if err != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("Read the catalogs the network of project %s reads: %v", entry.ID, err))
		}
		added := slices.DeleteFunc(slices.Clone(entry.CatalogIDs), func(c string) bool { return slices.Contains(network, c) })
		if len(added) != len(entry.CatalogIDs) {
			if err = s.pa.SetProjectCatalogs(ctx, entry.ID, added, entry.UpdateTime); err != nil {
				otellog.LogWarn(ctx, fmt.Sprintf("Store the added catalogs of project %s failed: %v", entry.ID, err))
			} else {
				entry.CatalogIDs = added
			}
		}
		s.ResolveCatalogs(ctx, token, entry, owner)
	}
	span.SetStatus(codes.Ok, "")
	return entry, nil
}

// packageMaterial is what a package restores, read and checked before the project is created.
type packageMaterial struct {
	docs              []packageDocument
	spec, draft       []byte
	hasSpec, hasDraft bool
}

// readMaterial checks that the documents, specification and draft of a package read as such.
func readMaterial(ctx context.Context, files map[string][]byte) (*packageMaterial, error) {
	m := &packageMaterial{}
	if raw, ok := files[packageDocuments]; ok {
		if err := json.Unmarshal(raw, &m.docs); err != nil {
			return nil, invalidPackage(ctx, err)
		}
	}
	if raw, ok := files[packageSpecification]; ok {
		if err := json.Unmarshal(raw, &packageSpec{}); err != nil {
			return nil, invalidPackage(ctx, err)
		}
		m.spec, m.hasSpec = raw, true
	}
	if raw, ok := files[packageDraft]; ok {
		if err := json.Unmarshal(raw, &interfaces.DraftItems{}); err != nil {
			return nil, invalidPackage(ctx, err)
		}
		m.draft, m.hasDraft = raw, true
	}
	return m, nil
}

// restore writes the package's material into the project made for it, ids of the exporting
// environment replaced by those of this one: catalogs as mapped, tables by name within them, and
// documents by the ids they get here. Only the fields that hold such ids are rewritten.
func (s *projectService) restore(ctx context.Context, token string, project *interfaces.Project,
	manifest *interfaces.PackageManifest, material *packageMaterial, files map[string][]byte,
	mapping map[string]string, owner interfaces.AccountInfo) error {
	ids := idRewrite{catalogs: map[string]string{}, tables: map[string]string{}, documents: map[string]string{}}
	for _, c := range manifest.Catalogs {
		if to := mapping[c.ID]; to != "" {
			ids.catalogs[c.ID] = to
		}
	}
	tablesOf := map[string]map[string]string{}
	for _, t := range manifest.Tables {
		target := mapping[t.CatalogID]
		if target == "" || t.Name == "" {
			continue
		}
		byName, ok := tablesOf[target]
		if !ok {
			byName = map[string]string{}
			tables, err := s.va.ListTables(ctx, owner, target)
			if err != nil {
				otellog.LogWarn(ctx, fmt.Sprintf("List tables of catalog %s failed: %v", target, err))
			}
			for _, vt := range tables {
				byName[vt.Name] = vt.ID
			}
			tablesOf[target] = byName
		}
		if id, ok := byName[t.Name]; ok {
			ids.tables[t.ID] = id
		}
	}

	now := time.Now().UnixMilli()
	{
		for _, d := range material.docs {
			id, err := uuid.NewV7()
			if err != nil {
				return internalError(ctx, "Generate document id failed", err)
			}
			sum := sha256.Sum256([]byte(d.Content))
			doc := &interfaces.RequirementDocument{
				ID: id.String(), ProjectID: project.ID, Name: d.Name, DocType: d.DocType, Format: d.Format,
				Content: d.Content, Blocks: d.Blocks, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(d.Content)),
				Uploader: owner, CreateTime: now, UpdateTime: now,
			}
			created, existing, err := s.rqa.CreateDocument(ctx, doc)
			if err != nil {
				return internalError(ctx, "Restore requirement document failed", err)
			}
			if !created {
				doc.ID = existing
			}
			if d.ID != "" {
				ids.documents[d.ID] = doc.ID
			}
		}
	}
	if material.hasSpec {
		spec := packageSpec{}
		if err := unmarshalRewritten(material.spec, ids, &spec); err != nil {
			return internalError(ctx, "Restore requirement failed", err)
		}
		runID, err := uuid.NewV7()
		if err != nil {
			return internalError(ctx, "Generate run id failed", err)
		}
		req := &interfaces.Requirement{
			ProjectID: project.ID, Version: spec.Version, Status: interfaces.REQUIREMENT_STATUS_DRAFTING,
			RunID: runID.String(), Drafter: owner, CreateTime: now, UpdateTime: now,
		}
		if _, err := s.rqa.StartDrafting(ctx, req, 0, 0); err != nil {
			return internalError(ctx, "Restore requirement failed", err)
		}
		req.Status, req.Spec, req.Inputs = interfaces.REQUIREMENT_STATUS_READY, spec.Spec, spec.Inputs
		if _, err := s.rqa.FinishDrafting(ctx, req); err != nil {
			return internalError(ctx, "Restore requirement failed", err)
		}
	}
	if material.hasDraft {
		items := interfaces.DraftItems{}
		if err := unmarshalRewritten(material.draft, ids, &items); err != nil {
			return internalError(ctx, "Restore draft failed", err)
		}
		// The draft comes back to be confirmed here: nothing of it is on this project's branch, and
		// nothing is being added or prepared.
		items.Confirmation, items.Adding, items.DatasetPlans = nil, nil, nil
		draft := &interfaces.Draft{ProjectID: project.ID, Status: interfaces.DRAFT_STATUS_DRAFTING, Drafter: owner,
			CreateTime: now, UpdateTime: now}
		if _, err := s.da.StartDraft(ctx, draft, 0, false); err != nil {
			return internalError(ctx, "Restore draft failed", err)
		}
		draft.Status, draft.Items = interfaces.DRAFT_STATUS_READY, items
		if err := s.da.FinishDraft(ctx, draft); err != nil {
			return internalError(ctx, "Restore draft failed", err)
		}
	}
	parts := map[string]map[string][]byte{}
	for p, b := range files {
		rest, ok := strings.CutPrefix(p, packageEditions)
		if !ok {
			continue
		}
		part, file, ok := strings.Cut(rest, "/")
		if !ok || !slices.Contains(manifest.Contents.Editions, part) {
			continue
		}
		if parts[part] == nil {
			parts[part] = map[string][]byte{}
		}
		parts[part][file] = b
	}
	if packager, ok := projecthooks.Current().Projects.(projecthooks.ProjectPackager); ok && len(parts) > 0 {
		if err := packager.ImportParts(ctx, token, project.ID, parts, ids.all(), owner); err != nil {
			return err
		}
	}
	return nil
}

// idRewrite maps ids of the exporting environment to those of this one, by what they name.
type idRewrite struct {
	catalogs, tables, documents map[string]string
}

// The JSON fields of a specification and a draft that hold catalog, table and document ids. A
// value equal to an old id elsewhere, such as an item id or a name, stays as it is.
var (
	catalogIDFields  = map[string]bool{"catalog_id": true, "catalog_ids": true, "added_by_catalog": true}
	tableIDFields    = map[string]bool{"resource_id": true, "resource_ids": true, "table_id": true}
	documentIDFields = map[string]bool{"document_id": true, "document_ids": true}
)

// all is every id mapped, for an edition's parts.
func (r idRewrite) all() map[string]string {
	out := map[string]string{}
	for _, m := range []map[string]string{r.catalogs, r.tables, r.documents} {
		for from, to := range m {
			out[from] = to
		}
	}
	return out
}

// unmarshalRewritten decodes raw into v with the id fields rewritten.
func unmarshalRewritten(raw []byte, r idRewrite, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return err
	}
	b, err := json.Marshal(r.walk(doc, "", ""))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// walk rewrites the ids of v, which sits under field key of an object under field parent.
func (r idRewrite) walk(v any, key, parent string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			t[k] = r.walk(child, k, key)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = r.walk(child, key, parent)
		}
		return t
	case string:
		var m map[string]string
		switch {
		case catalogIDFields[key]:
			m = r.catalogs
		case tableIDFields[key]:
			m = r.tables
		case documentIDFields[key], key == "id" && parent == "documents":
			// The documents a specification was drafted from are listed by id.
			m = r.documents
		}
		if to, ok := m[t]; ok {
			return to
		}
		return t
	}
	return v
}

// writePackage zips the manifest and files, recording each file's SHA-256 in the manifest.
func writePackage(manifest *interfaces.PackageManifest, files map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(files))
	manifest.Files = map[string]string{}
	for name, b := range files {
		names = append(names, name)
		sum := sha256.Sum256(b)
		manifest.Files[name] = hex.EncodeToString(sum[:])
	}
	sort.Strings(names)
	m, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for _, entry := range append([]string{packageManifest}, names...) {
		w, err := zw.Create(entry)
		if err != nil {
			return nil, err
		}
		b := m
		if entry != packageManifest {
			b = files[entry]
		}
		if _, err = w.Write(b); err != nil {
			return nil, err
		}
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readPackage checks a package's size, format, version and files, and returns its manifest and
// files.
func readPackage(ctx context.Context, pkg []byte) (*interfaces.PackageManifest, map[string][]byte, error) {
	if len(pkg) > interfaces.PROJECT_PACKAGE_MAX_BYTES {
		return nil, nil, tooLarge(ctx)
	}
	zr, err := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		return nil, nil, invalidPackage(ctx, err)
	}
	if len(zr.File) > packageMaxFiles {
		return nil, nil, invalidPackage(ctx, fmt.Errorf("%d files", len(zr.File)))
	}
	files := map[string][]byte{}
	total := int64(0)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := path.Clean(f.Name)
		if name != f.Name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") {
			return nil, nil, invalidPackage(ctx, fmt.Errorf("file name %q", f.Name))
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, invalidPackage(ctx, err)
		}
		// Unpacked, a package may not grow past what an import reads either.
		b, err := io.ReadAll(io.LimitReader(rc, interfaces.PROJECT_PACKAGE_MAX_BYTES-total+1))
		_ = rc.Close()
		if err != nil {
			return nil, nil, invalidPackage(ctx, err)
		}
		if total += int64(len(b)); total > interfaces.PROJECT_PACKAGE_MAX_BYTES {
			return nil, nil, tooLarge(ctx)
		}
		files[name] = b
	}
	raw, ok := files[packageManifest]
	if !ok {
		return nil, nil, invalidPackage(ctx, fmt.Errorf("no %s", packageManifest))
	}
	manifest := &interfaces.PackageManifest{}
	if err := json.Unmarshal(raw, manifest); err != nil || manifest.Format != interfaces.PROJECT_PACKAGE_FORMAT {
		return nil, nil, invalidPackage(ctx, fmt.Errorf("manifest: %v", err))
	}
	if manifest.FormatVersion != interfaces.PROJECT_PACKAGE_VERSION {
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_UnsupportedPackageVersion).
			WithErrorDetails(map[string]any{"format_version": manifest.FormatVersion, "supported": interfaces.PROJECT_PACKAGE_VERSION})
	}
	if manifest.NamingLanguage == "" {
		manifest.NamingLanguage = interfaces.NAMING_LANGUAGE_ZH_CN
	}
	if !interfaces.IsNamingLanguage(manifest.NamingLanguage) || strings.TrimSpace(manifest.Name) == "" {
		return nil, nil, invalidPackage(ctx, fmt.Errorf("manifest names %q in %q", manifest.Name, manifest.NamingLanguage))
	}
	delete(files, packageManifest)
	for name, b := range files {
		sum := sha256.Sum256(b)
		if manifest.Files[name] != hex.EncodeToString(sum[:]) {
			return nil, nil, invalidPackage(ctx, fmt.Errorf("file %s does not match the manifest", name))
		}
	}
	for name := range manifest.Files {
		if _, ok := files[name]; !ok {
			return nil, nil, invalidPackage(ctx, fmt.Errorf("file %s missing", name))
		}
	}
	if manifest.Catalogs == nil {
		manifest.Catalogs = []interfaces.PackageCatalog{}
	}
	if manifest.Contents.Editions == nil {
		manifest.Contents.Editions = []string{}
	}
	return manifest, files, nil
}

func invalidPackage(ctx context.Context, err error) error {
	otellog.LogWarn(ctx, fmt.Sprintf("Invalid project package: %v", err))
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidPackage)
}

func tooLarge(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusRequestEntityTooLarge, perrors.BknProject_Project_PackageTooLarge).
		WithErrorDetails(map[string]any{"max_bytes": interfaces.PROJECT_PACKAGE_MAX_BYTES})
}

// validPartName keeps an edition's part to one plain folder of the package.
func validPartName(part string) bool {
	return part != "" && !strings.ContainsAny(part, `/\.`)
}

// packageFileName is the project's name made safe as a file name, with the package extension.
func packageFileName(name string) string {
	safe := strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if safe == "" {
		safe = "project"
	}
	return safe + interfaces.PROJECT_PACKAGE_EXTENSION
}
