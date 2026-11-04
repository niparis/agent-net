package protocol

// TransferPartSize is the part size of the gateway's transfer in parts: 1 MB.
// A part travels as one JSON frame, so its base64 body is about 1.4 MB, well
// under the 4 MB WebSocket frame. The same transfer carries a file between
// machines and a skill above the 2 MB frame limit (decision 0048).
const TransferPartSize = 1 << 20

// TransferBegin starts an upload. The caller names the receiving machine, the
// whole size and the whole SHA-256. The gateway answers with a TransferState
// and an upload ID; the same ID is used for every later frame.
type TransferBegin struct {
	Receiver string `json:"receiver"`
	Name     string `json:"name,omitempty"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

// TransferState is the answer to xfer.begin and xfer.status. Have lists the
// part indexes the gateway already holds, so a resumed upload sends only the
// missing ones. ExpiresAt is a Unix time in seconds.
type TransferState struct {
	UploadID  string `json:"upload_id"`
	Name      string `json:"name,omitempty"`
	Receiver  string `json:"receiver"`
	PartSize  int    `json:"part_size"`
	Parts     int    `json:"parts"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Have      []int  `json:"have"`
	Received  int64  `json:"received"`
	Finished  bool   `json:"finished"`
	ExpiresAt int64  `json:"expires_at"`
}

// TransferStatus asks which parts of an upload the gateway holds.
type TransferStatus struct {
	UploadID string `json:"upload_id"`
}

// TransferPart carries one part. Data is the part bytes (base64 in JSON) and
// SHA256 is their SHA-256, so a damaged part is refused and retried.
type TransferPart struct {
	UploadID string `json:"upload_id"`
	Index    int    `json:"index"`
	SHA256   string `json:"sha256"`
	Data     []byte `json:"data"`
}

// TransferPartResult is the answer to xfer.part: how many parts are held and
// how many bytes they hold together.
type TransferPartResult struct {
	Have     int   `json:"have"`
	Received int64 `json:"received"`
}

// TransferFinish asks the gateway to check the whole SHA-256 of the upload.
type TransferFinish struct {
	UploadID string `json:"upload_id"`
}

// TransferFinished is the answer to xfer.finish.
type TransferFinished struct {
	UploadID string `json:"upload_id"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

// TransferGet asks for one part of a finished upload. Only the receiving
// machine may ask.
type TransferGet struct {
	UploadID string `json:"upload_id"`
	Index    int    `json:"index"`
}

// TransferChunk is one downloaded part, with the part hash and the whole hash
// so the receiver can check what it assembled.
type TransferChunk struct {
	UploadID    string `json:"upload_id"`
	Index       int    `json:"index"`
	Parts       int    `json:"parts"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	TotalSHA256 string `json:"total_sha256"`
	Data        []byte `json:"data"`
}

// TransferConfirm says the receiving machine has the whole file. The gateway
// deletes the stored parts at once.
type TransferConfirm struct {
	UploadID string `json:"upload_id"`
}

// TransferConfirmed is the answer to xfer.confirm.
type TransferConfirmed struct {
	UploadID string `json:"upload_id"`
	Deleted  bool   `json:"deleted"`
}

// TransferFind asks for the finished uploads addressed to this machine under
// the given names. A file message names each upload with the file's unique
// name (TaildropName), so the receiver finds the bytes without an upload ID.
// At most MaxFilesPerMessage names.
type TransferFind struct {
	Names []string `json:"names"`
}

// TransferFound is the answer to xfer.find: the finished uploads addressed to
// the calling machine whose name is one of the names asked for.
type TransferFound struct {
	Uploads []TransferState `json:"uploads"`
}
