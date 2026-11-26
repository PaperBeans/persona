package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// FileTransferAudit is one row of file_transfer_audit: what an Agent or an assistant conversation moved between two
// of the user's computers with file_transfer (docs/protocol/file-transfer.md "Agents and the assistant"). Paths and
// sizes only, never a file's content or a credential.
type FileTransferAudit struct {
	ID            string    `json:"id"`
	At            time.Time `json:"at"`
	CallerMachine string    `json:"caller_machine"`
	CallerSession string    `json:"caller_session,omitempty"`
	CallerKind    string    `json:"caller_kind"` // agent or assistant
	Direction     string    `json:"direction"`   // send or fetch
	FromMachine   string    `json:"from_machine"`
	FromPath      string    `json:"from_path"`
	ToMachine     string    `json:"to_machine"`
	ToPath        string    `json:"to_path,omitempty"` // as asked; "" the receive folder
	ResultPath    string    `json:"result_path,omitempty"`
	Kind          string    `json:"kind,omitempty"` // file or dir
	Bytes         int64     `json:"bytes"`
	Files         int       `json:"files,omitempty"`
	Skipped       int       `json:"skipped,omitempty"`
	SHA256        string    `json:"sha256,omitempty"`
	State         string    `json:"state"` // done or failed
	Code          string    `json:"code,omitempty"`
	Error         string    `json:"error,omitempty"`
	ElapsedMS     int64     `json:"elapsed_ms"`
}

// AddFileTransferAudit records one Agent's transfer; At zero is now.
func (d *DB) AddFileTransferAudit(ctx context.Context, a FileTransferAudit) error {
	if a.ID == "" || a.FromMachine == "" || a.ToMachine == "" || a.State == "" {
		return errors.New("file transfer audit: id, machines and state are required")
	}
	if a.At.IsZero() {
		a.At = time.Now()
	}
	_, err := d.pool.Exec(ctx, `INSERT INTO file_transfer_audit (id, at, caller_machine, caller_session, caller_kind, direction,
		from_machine, from_path, to_machine, to_path, result_path, kind, bytes, files, skipped, sha256, state, code, error, elapsed_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
		a.ID, a.At, a.CallerMachine, a.CallerSession, a.CallerKind, a.Direction, a.FromMachine, a.FromPath, a.ToMachine,
		a.ToPath, a.ResultPath, a.Kind, a.Bytes, a.Files, a.Skipped, a.SHA256, a.State, a.Code, a.Error, a.ElapsedMS)
	return err
}

// FileTransferAudits are the latest rows, newest first, at most limit (1–500; 100 when out of range), those of machine
// (as either end) when it is not "".
func (d *DB) FileTransferAudits(ctx context.Context, machine string, limit int) ([]FileTransferAudit, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.pool.Query(ctx, `SELECT id, at, caller_machine, caller_session, caller_kind, direction, from_machine, from_path,
		to_machine, to_path, result_path, kind, bytes, files, skipped, sha256, state, code, error, elapsed_ms
		FROM file_transfer_audit WHERE $1 = '' OR from_machine = $1 OR to_machine = $1 ORDER BY at DESC, id LIMIT $2`, machine, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FileTransferAudit, error) {
		var a FileTransferAudit
		err := r.Scan(&a.ID, &a.At, &a.CallerMachine, &a.CallerSession, &a.CallerKind, &a.Direction, &a.FromMachine, &a.FromPath,
			&a.ToMachine, &a.ToPath, &a.ResultPath, &a.Kind, &a.Bytes, &a.Files, &a.Skipped, &a.SHA256, &a.State, &a.Code,
			&a.Error, &a.ElapsedMS)
		return a, err
	})
}
