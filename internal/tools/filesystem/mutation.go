package filesystem

import (
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type MutationOutput struct {
	Path          string   `json:"path"`
	OldHash       string   `json:"old_hash,omitempty"`
	NewHash       string   `json:"new_hash"`
	ChangedRanges []string `json:"changed_ranges,omitempty"`
	Diff          string   `json:"diff,omitempty"`
	Truncated     bool     `json:"truncated,omitempty"`
	BytesWritten  int64    `json:"bytes_written"`
}

func mutationResult(relPath string, before, after []byte) MutationOutput {
	oldHash := ""
	if before != nil {
		oldHash = fileHash(before)
	}
	diff, ranges, truncated := tools.CompactFileDiff(relPath, before, after)
	return MutationOutput{
		Path:          relPath,
		OldHash:       oldHash,
		NewHash:       fileHash(after),
		ChangedRanges: ranges,
		Diff:          diff,
		Truncated:     truncated,
		BytesWritten:  int64(len(after)),
	}
}
