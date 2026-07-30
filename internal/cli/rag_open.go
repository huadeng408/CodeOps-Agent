package cli

import "errors"

var (
	errIngestPathOutsideWorkspace = errors.New("ingest path is outside workspace")
	errIngestPathNotRegular       = errors.New("ingest path is not a regular file")
)
