package proto

import "time"

// Computer to computer transfers (POST /api/v1/files/transfer, docs/protocol/file-transfer.md): the hub reads the file
// from one machine (file.chunk, pinned to the revision it saw first) and writes it into another's receive folder
// (file.upload.* with dest=inbox) a chunk at a time; nothing is written to the hub's disk.

// FileTransferRequest is the body of POST /api/v1/files/transfer.
type FileTransferRequest struct {
	From FileTransferSource `json:"from"`
	To   FileTransferTarget `json:"to"`
}

type FileTransferSource struct {
	Machine string `json:"machine"`
	Path    string `json:"path"`
	Cwd     string `json:"cwd,omitempty"` // what a relative Path is relative to, as in file/download
}

type FileTransferTarget struct {
	Machine string `json:"machine"`
	Dest    string `json:"dest"` // UploadDestInbox, the only one for now
}

// The states of a transfer: running until every byte is written and the file has its name (done), or until a side
// fails or the client cancels it.
const (
	TransferRunning   = "running"
	TransferDone      = "done"
	TransferFailed    = "failed"
	TransferCancelled = "cancelled"
)

// FileTransfer is one transfer as the hub knows it (the answer of POST and GET, the file_transfer event's transfer).
// It lives in the hub's memory: a hub restart forgets it, and GET then answers 404 (the client sends it again).
type FileTransfer struct {
	ID         string    `json:"id"`
	State      string    `json:"state"`
	From       string    `json:"from"` // the source machine
	To         string    `json:"to"`   // the target machine
	Name       string    `json:"name"` // the file's name on the source
	Bytes      int64     `json:"bytes"`
	Total      int64     `json:"total"`
	Error      string    `json:"error,omitempty"`
	Code       string    `json:"code,omitempty"`        // set with some errors: CodeFileChanged, CodeTooLarge, CodeNodeOutdated…
	ResultPath string    `json:"result_path,omitempty"` // done: the absolute path on the target
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CodeFileChanged: the source file changed while it was being sent (its revision is not the one the transfer began
// with); the client may start it again.
const CodeFileChanged = "file_changed"

// FileTransferEvent is the WebSocket event's type; it goes only to the apps that declared file_transfer.
const FileTransferEvent = "file_transfer"

// MachineFileTransfer is a machine's file transfer switch (settings key machine_file_transfer, off until the user
// turns it on): Inbox is its receive folder as hub.json names it ("~/…" or absolute), set while it is on.
type MachineFileTransfer struct {
	Enabled bool   `json:"enabled"`
	Inbox   string `json:"inbox,omitempty"`
}

// CodeFileTransferDisabled: the machine's file transfer switch is off (403): a dest=inbox upload to it, or a transfer
// from or to it. Chat attachments and file previews / downloads do not depend on it.
const CodeFileTransferDisabled = "file_transfer_disabled"

// Agents' transfers (file_transfer, docs/protocol/file-transfer.md "Agents and the assistant"): the manager has the
// source's node prepare what it sends (file.pack: a file as it is, a folder zipped into a temporary file, either with
// its SHA-256), streams it with file.chunk into file.upload.* on the target (dest=path or inbox, with sha256 and, for a
// folder, unpack), and has the source remove the temporary zip (file.pack.done).
const (
	MethodFilePack     = "file.pack"
	MethodFilePackDone = "file.pack.done"
)

// The kinds of what file.pack prepares.
const (
	FilePackFile = "file"
	FilePackDir  = "dir"
)

// MaxAgentTransferBytes bounds what one Agent's transfer moves: a file, or a folder's zip.
const MaxAgentTransferBytes = MaxInboxUploadBytes

// CodeChecksumMismatch: the bytes written on the target do not hash to what the source sent; nothing was kept.
const CodeChecksumMismatch = "checksum_mismatch"

// CodeTransferSecret: the path is a credential by its name (identity.TransferSecretName), which an Agent's transfer
// does not read.
const CodeTransferSecret = "credential"

// FilePackRequest is file.pack's params: the file or folder to send, "~/", absolute or relative to Cwd.
type FilePackRequest struct {
	Path string `json:"path"`
	Cwd  string `json:"cwd,omitempty"`
}

// FilePack is what file.pack prepared: Path is what file.chunk reads (the file itself, or the zip the folder was
// packed into, Temp), at Revision, Size bytes hashing to SHA256. Name is the file's or the folder's own name, Source
// the path it was found at.
type FilePack struct {
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	Size     int64  `json:"size"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
	Temp     bool   `json:"temp,omitempty"`
	// A folder's: the files packed and their bytes before packing, and what was left out (credentials, links to
	// outside it, what could not be read): how many, and the first few by their path in the folder.
	Files        int      `json:"files,omitempty"`
	Bytes        int64    `json:"bytes,omitempty"`
	Skipped      int      `json:"skipped,omitempty"`
	SkippedNames []string `json:"skipped_names,omitempty"`
}
