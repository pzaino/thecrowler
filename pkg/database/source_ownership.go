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
}

// GetSourceOwner looks up the persisted owner of a live source.
//
// The query intentionally uses only Sources.usr_id. The Owners and
// SourceOwnerIndex tables are not maintained by the source lifecycle and must
// not be treated as authoritative ownership data until a separate contract and
// migration are approved.
func GetSourceOwner(ctx context.Context, db *Handler, sourceID uint64) (SourceOwner, error) {
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
	if dbms != DBMySQLStr {
		deletedFilter = "\n\t\t  AND deleted_at IS NULL"
	}
	query := `SELECT source_id, usr_id FROM Sources
		WHERE source_id = ` + placeholder + deletedFilter

	var owner SourceOwner
	err := (*db).QueryRowContext(ctx, query, sourceID).Scan(&owner.SourceID, &owner.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceOwner{SourceID: sourceID}, nil
	}
	if err != nil {
		return SourceOwner{}, fmt.Errorf("look up source owner: %w", err)
	}
	owner.Found = true
	return owner, nil
}
