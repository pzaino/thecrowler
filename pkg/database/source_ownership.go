package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SourceOwner is the persisted owner information for one source.
//
// CROWler currently stores the creating user in Sources.usr_id. A zero user ID
// means the ownership is unknown or was never recorded; callers must treat it
// as fail-closed rather than as a shared owner.
type SourceOwner struct {
	SourceID uint64
	UserID   uint64
	Found    bool
	Deleted  bool
}

// GetSourceOwner looks up the persisted owner of a live source.
//
// The query intentionally uses only Sources.usr_id. The Owners and
// SourceOwnerIndex tables are not maintained by the source lifecycle and must
// not be treated as authoritative ownership data until a separate contract and
// migration are approved.
func GetSourceOwner(ctx context.Context, db *Handler, sourceID uint64) (SourceOwner, error) {
	return getSourceOwner(ctx, db, sourceID, false)
}

func getSourceOwner(ctx context.Context, db *Handler, sourceID uint64, includeDeleted bool) (SourceOwner, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return SourceOwner{}, fmt.Errorf("database handler is nil")
	}
	if sourceID == 0 {
		return SourceOwner{}, fmt.Errorf("source ID is required")
	}

	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	placeholder := "?"
	if dbms == DBPostgresStr {
		placeholder = "$1"
	}
	deletedFilter := ""
	selectDeleted := ""
	if dbms != DBMySQLStr {
		if !includeDeleted {
			deletedFilter = " AND deleted_at IS NULL"
		}
		selectDeleted = ", deleted_at IS NOT NULL"
	}
	query := `SELECT source_id, usr_id` + selectDeleted + ` FROM Sources
		WHERE source_id = ` + placeholder + deletedFilter

	var owner SourceOwner
	var deleted bool
	scanArgs := []any{&owner.SourceID, &owner.UserID}
	if selectDeleted != "" {
		scanArgs = append(scanArgs, &deleted)
	}
	err := (*db).QueryRowContext(ctx, query, sourceID).Scan(scanArgs...)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceOwner{SourceID: sourceID}, nil
	}
	if err != nil {
		return SourceOwner{}, fmt.Errorf("look up source owner: %w", err)
	}
	owner.Found = true
	owner.Deleted = deleted
	return owner, nil
}

// GetSourceOwnerByURL resolves a source by canonical URL. When includeDeleted
// is true, deleted rows are returned with Deleted=true so callers can prevent
// a URL upsert from crossing an existing ownership boundary.
func GetSourceOwnerByURL(ctx context.Context, db *Handler, sourceURL string, includeDeleted bool) (SourceOwner, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return SourceOwner{}, fmt.Errorf("database handler is nil")
	}
	sourceURL = NormalizeSourceURL(sourceURL)
	if sourceURL == "" {
		return SourceOwner{}, fmt.Errorf("source URL is required")
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	placeholder := "?"
	if dbms == DBPostgresStr {
		placeholder = "$1"
	}
	deletedFilter := ""
	selectDeleted := ""
	if dbms != DBMySQLStr {
		if !includeDeleted {
			deletedFilter = " AND deleted_at IS NULL"
		}
		selectDeleted = ", deleted_at IS NOT NULL"
	}
	query := `SELECT source_id, usr_id` + selectDeleted + ` FROM Sources
		WHERE url = ` + placeholder + deletedFilter + ` LIMIT 1`
	var owner SourceOwner
	var deleted bool
	scanArgs := []any{&owner.SourceID, &owner.UserID}
	if selectDeleted != "" {
		scanArgs = append(scanArgs, &deleted)
	}
	err := (*db).QueryRowContext(ctx, query, sourceURL).Scan(scanArgs...)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceOwner{}, nil
	}
	if err != nil {
		return SourceOwner{}, fmt.Errorf("look up source owner by URL: %w", err)
	}
	owner.Found = true
	owner.Deleted = deleted
	return owner, nil
}
