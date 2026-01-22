package github

import (
	"fmt"

	"github.com/github/github-mcp-server/pkg/utils"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Chunk safety margin - leave 20% below the 100MB limit for API overhead
const ChunkSafetyMarginPercent = 0.80

// FileEntry represents a file to be pushed with its path and content
type FileEntry struct {
	Path    string
	Content string
}

// FileValidationResult contains detailed validation results
type FileValidationResult struct {
	TotalSize       int64
	FileCount       int
	LargestFile     string
	LargestFileSize int64
	Duplicates      map[string][]int // path -> indices where duplicates found
	OversizedFiles  []string         // files exceeding MaxFileSizeBytes
}

// ValidationError provides detailed error information with suggestions
type ValidationError struct {
	Code       string
	Message    string
	Suggestion string
	Details    map[string]interface{}
}

func (e *ValidationError) Error() string {
	if e.Suggestion != "" {
		return fmt.Sprintf("%s. Suggestion: %s", e.Message, e.Suggestion)
	}
	return e.Message
}

// ValidateFiles performs comprehensive validation on a set of files
func ValidateFiles(files []interface{}) (*FileValidationResult, []FileEntry, error) {
	result := &FileValidationResult{
		Duplicates:     make(map[string][]int),
		OversizedFiles: make([]string, 0),
	}

	seenPaths := make(map[string]int)
	entries := make([]FileEntry, 0, len(files))

	for i, file := range files {
		fileMap, ok := file.(map[string]interface{})
		if !ok {
			return nil, nil, &ValidationError{
				Code:    "INVALID_FILE_FORMAT",
				Message: fmt.Sprintf("file at index %d must be an object with path and content", i),
				Suggestion: "Ensure each file has both 'path' (string) and 'content' (string) fields",
			}
		}

		path, ok := fileMap["path"].(string)
		if !ok || path == "" {
			return nil, nil, &ValidationError{
				Code:    "MISSING_FILE_PATH",
				Message: fmt.Sprintf("file at index %d must have a non-empty path", i),
				Suggestion: "Add a valid 'path' field to each file object",
			}
		}

		content, ok := fileMap["content"].(string)
		if !ok {
			return nil, nil, &ValidationError{
				Code:    "MISSING_FILE_CONTENT",
				Message: fmt.Sprintf("file at index %d must have content", i),
				Suggestion: "Add a 'content' field to the file object (can be empty string)",
			}
		}

		// Check for duplicate paths
		if firstIndex, exists := seenPaths[path]; exists {
			if _, tracked := result.Duplicates[path]; !tracked {
				result.Duplicates[path] = []int{firstIndex}
			}
			result.Duplicates[path] = append(result.Duplicates[path], i)
		}
		seenPaths[path] = i

		// Calculate sizes
		fileSize := int64(len(content))
		result.TotalSize += fileSize
		result.FileCount++

		// Track largest file
		if fileSize > result.LargestFileSize {
			result.LargestFile = path
			result.LargestFileSize = fileSize
		}

		// Track oversized files
		if fileSize > MaxFileSizeBytes {
			result.OversizedFiles = append(result.OversizedFiles, path)
		}

		entries = append(entries, FileEntry{
			Path:    path,
			Content: content,
		})
	}

	// Check for duplicates
	if len(result.Duplicates) > 0 {
		firstDup := ""
		var indices []int
		for path, idxs := range result.Duplicates {
			firstDup = path
			indices = idxs
			break
		}
		return result, nil, &ValidationError{
			Code:    "DUPLICATE_FILE_PATHS",
			Message: fmt.Sprintf("duplicate file path '%s' found at indices %v - each file path must be unique", firstDup, indices),
			Suggestion: fmt.Sprintf("Remove duplicate entries for '%s' and ensure each path appears only once", firstDup),
			Details: map[string]interface{}{
				"duplicates": result.Duplicates,
			},
		}
	}

	return result, entries, nil
}

// ValidateFileCount checks if file count is within limits
func ValidateFileCount(count int, maxFiles int) (*mcp.CallToolResult, error) {
	if count > maxFiles {
		return utils.NewToolResultError(fmt.Sprintf(
			"too many files: %d exceeds maximum of %d per push_files call. Use push_files_chunked for larger batches or make multiple calls",
			count, maxFiles,
		)), &ValidationError{
			Code:       "TOO_MANY_FILES",
			Message:    fmt.Sprintf("file count %d exceeds maximum %d", count, maxFiles),
			Suggestion: "Use push_files_chunked tool for batches over 100 files, or split into multiple push_files calls",
		}
	}
	return nil, nil
}

// ValidateFileSize checks if individual file size is within limits
func ValidateFileSize(path string, size int64) (*mcp.CallToolResult, error) {
	if size > MaxFileSizeBytes {
		sizeMB := float64(size) / (1024 * 1024)
		maxMB := float64(MaxFileSizeBytes) / (1024 * 1024)
		return utils.NewToolResultError(fmt.Sprintf(
			"file '%s' size (%d bytes, %.2f MB) exceeds maximum of %d bytes (%.0f MB)",
			path, size, sizeMB, MaxFileSizeBytes, maxMB,
		)), &ValidationError{
			Code:    "FILE_TOO_LARGE",
			Message: fmt.Sprintf("file '%s' is %.2f MB, exceeds limit of %.0f MB", path, sizeMB, maxMB),
			Suggestion: fmt.Sprintf("Split '%s' into smaller files or use Git LFS for large files", path),
			Details: map[string]interface{}{
				"file_size_bytes": size,
				"file_size_mb":    sizeMB,
				"max_bytes":       MaxFileSizeBytes,
				"max_mb":          maxMB,
			},
		}
	}
	return nil, nil
}

// ValidateTotalSize checks if total size of all files is within limits
func ValidateTotalSize(totalSize int64) (*mcp.CallToolResult, error) {
	if totalSize > MaxTotalPushSizeBytes {
		sizeMB := float64(totalSize) / (1024 * 1024)
		maxMB := float64(MaxTotalPushSizeBytes) / (1024 * 1024)
		return utils.NewToolResultError(fmt.Sprintf(
			"total content size (%d bytes, %.2f MB) exceeds maximum of %d bytes (%.0f MB)",
			totalSize, sizeMB, MaxTotalPushSizeBytes, maxMB,
		)), &ValidationError{
			Code:    "TOTAL_SIZE_TOO_LARGE",
			Message: fmt.Sprintf("total size %.2f MB exceeds limit of %.0f MB", sizeMB, maxMB),
			Suggestion: "Use push_files_chunked to split into multiple commits, or reduce the number of files per push",
			Details: map[string]interface{}{
				"total_size_bytes": totalSize,
				"total_size_mb":    sizeMB,
				"max_bytes":        MaxTotalPushSizeBytes,
				"max_mb":           maxMB,
			},
		}
	}
	return nil, nil
}

// ValidateChunkSize validates that a chunk doesn't exceed size limits
func ValidateChunkSize(files []FileEntry) error {
	var chunkSize int64
	for _, file := range files {
		chunkSize += int64(len(file.Content))
	}

	if chunkSize > MaxTotalPushSizeBytes {
		sizeMB := float64(chunkSize) / (1024 * 1024)
		maxMB := float64(MaxTotalPushSizeBytes) / (1024 * 1024)
		return &ValidationError{
			Code:    "CHUNK_TOO_LARGE",
			Message: fmt.Sprintf("chunk size (%.2f MB) exceeds maximum of %.0f MB - this chunk contains %d files totaling too much data", sizeMB, maxMB, len(files)),
			Suggestion: "Reduce chunk_size parameter to use smaller chunks",
			Details: map[string]interface{}{
				"chunk_size_bytes": chunkSize,
				"chunk_size_mb":    sizeMB,
				"max_bytes":        MaxTotalPushSizeBytes,
				"max_mb":           maxMB,
				"file_count":       len(files),
			},
		}
	}

	return nil
}

// GetMaxChunkSize returns the maximum safe chunk size with safety margin
func GetMaxChunkSize() int64 {
	return int64(float64(MaxTotalPushSizeBytes) * ChunkSafetyMarginPercent)
}

// FormatFileSize formats bytes as human-readable size
func FormatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
