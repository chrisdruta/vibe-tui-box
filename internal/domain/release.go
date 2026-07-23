package domain

// ReleaseProvenance records where a release artifact came from and how
// it was verified. It lives in domain so the store can persist it
// without importing the release package.
type ReleaseProvenance struct {
	Source      string `json:"source"`       // canonical release source URL
	Version     string `json:"version"`      // release tag, e.g. "v2.0.0"
	Attestation string `json:"attestation"`  // verification method identifier, "" when unverified
	ArchiveHash Digest `json:"archive_hash"` // digest of the downloaded archive
}
