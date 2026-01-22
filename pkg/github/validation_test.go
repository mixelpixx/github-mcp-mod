package github

import (
	"strings"
	"testing"
)

func TestValidateFiles_Success(t *testing.T) {
	files := []interface{}{
		map[string]interface{}{
			"path":    "file1.txt",
			"content": "content1",
		},
		map[string]interface{}{
			"path":    "file2.txt",
			"content": "content2",
		},
	}

	result, entries, err := ValidateFiles(files)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.FileCount != 2 {
		t.Errorf("expected 2 files, got %d", result.FileCount)
	}

	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Path != "file1.txt" || entries[0].Content != "content1" {
		t.Errorf("unexpected entry values for file 1")
	}

	expectedSize := int64(len("content1") + len("content2"))
	if result.TotalSize != expectedSize {
		t.Errorf("expected total size %d, got %d", expectedSize, result.TotalSize)
	}
}

func TestValidateFiles_DuplicatePaths(t *testing.T) {
	files := []interface{}{
		map[string]interface{}{
			"path":    "file.txt",
			"content": "content1",
		},
		map[string]interface{}{
			"path":    "file.txt",
			"content": "content2",
		},
	}

	_, _, err := ValidateFiles(files)
	if err == nil {
		t.Fatal("expected error for duplicate paths, got nil")
	}

	validationErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T", err)
	}

	if validationErr.Code != "DUPLICATE_FILE_PATHS" {
		t.Errorf("expected code DUPLICATE_FILE_PATHS, got %s", validationErr.Code)
	}

	if !strings.Contains(validationErr.Message, "file.txt") {
		t.Errorf("error message should mention the duplicate file path")
	}
}

func TestValidateFiles_MissingPath(t *testing.T) {
	files := []interface{}{
		map[string]interface{}{
			"content": "content",
		},
	}

	_, _, err := ValidateFiles(files)
	if err == nil {
		t.Fatal("expected error for missing path, got nil")
	}

	validationErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T", err)
	}

	if validationErr.Code != "MISSING_FILE_PATH" {
		t.Errorf("expected code MISSING_FILE_PATH, got %s", validationErr.Code)
	}
}

func TestValidateFiles_EmptyPath(t *testing.T) {
	files := []interface{}{
		map[string]interface{}{
			"path":    "",
			"content": "content",
		},
	}

	_, _, err := ValidateFiles(files)
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}

	validationErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T", err)
	}

	if validationErr.Code != "MISSING_FILE_PATH" {
		t.Errorf("expected code MISSING_FILE_PATH, got %s", validationErr.Code)
	}
}

func TestValidateFiles_MissingContent(t *testing.T) {
	files := []interface{}{
		map[string]interface{}{
			"path": "file.txt",
		},
	}

	_, _, err := ValidateFiles(files)
	if err == nil {
		t.Fatal("expected error for missing content, got nil")
	}

	validationErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T", err)
	}

	if validationErr.Code != "MISSING_FILE_CONTENT" {
		t.Errorf("expected code MISSING_FILE_CONTENT, got %s", validationErr.Code)
	}
}

func TestValidateFiles_InvalidFormat(t *testing.T) {
	files := []interface{}{
		"not a map",
	}

	_, _, err := ValidateFiles(files)
	if err == nil {
		t.Fatal("expected error for invalid format, got nil")
	}

	validationErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T", err)
	}

	if validationErr.Code != "INVALID_FILE_FORMAT" {
		t.Errorf("expected code INVALID_FILE_FORMAT, got %s", validationErr.Code)
	}
}

func TestValidateFiles_LargestFileTracking(t *testing.T) {
	files := []interface{}{
		map[string]interface{}{
			"path":    "small.txt",
			"content": "small",
		},
		map[string]interface{}{
			"path":    "large.txt",
			"content": strings.Repeat("x", 1000),
		},
		map[string]interface{}{
			"path":    "medium.txt",
			"content": strings.Repeat("x", 100),
		},
	}

	result, _, err := ValidateFiles(files)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.LargestFile != "large.txt" {
		t.Errorf("expected largest file to be large.txt, got %s", result.LargestFile)
	}

	if result.LargestFileSize != 1000 {
		t.Errorf("expected largest file size 1000, got %d", result.LargestFileSize)
	}
}

func TestValidateFiles_OversizedFiles(t *testing.T) {
	largeContent := strings.Repeat("x", int(MaxFileSizeBytes+1))
	files := []interface{}{
		map[string]interface{}{
			"path":    "too-large.txt",
			"content": largeContent,
		},
		map[string]interface{}{
			"path":    "normal.txt",
			"content": "normal",
		},
	}

	result, _, err := ValidateFiles(files)
	if err != nil {
		t.Fatalf("expected no error from ValidateFiles, got %v", err)
	}

	if len(result.OversizedFiles) != 1 {
		t.Errorf("expected 1 oversized file, got %d", len(result.OversizedFiles))
	}

	if result.OversizedFiles[0] != "too-large.txt" {
		t.Errorf("expected oversized file to be too-large.txt, got %s", result.OversizedFiles[0])
	}
}

func TestValidateFileCount(t *testing.T) {
	tests := []struct {
		name      string
		count     int
		maxFiles  int
		expectErr bool
	}{
		{"within limit", 50, 100, false},
		{"at limit", 100, 100, false},
		{"exceed limit", 101, 100, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ValidateFileCount(tt.count, tt.maxFiles)
			if tt.expectErr {
				if result == nil && err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if result != nil || err != nil {
					t.Errorf("expected no error, got result=%v, err=%v", result, err)
				}
			}
		})
	}
}

func TestValidateFileSize(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		size      int64
		expectErr bool
	}{
		{"small file", "small.txt", 1024, false},
		{"at limit", "at-limit.txt", MaxFileSizeBytes, false},
		{"exceed limit", "too-large.txt", MaxFileSizeBytes + 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ValidateFileSize(tt.path, tt.size)
			if tt.expectErr {
				if result == nil && err == nil {
					t.Error("expected error, got nil")
				}
				validationErr, ok := err.(*ValidationError)
				if ok {
					if validationErr.Code != "FILE_TOO_LARGE" {
						t.Errorf("expected code FILE_TOO_LARGE, got %s", validationErr.Code)
					}
					if !strings.Contains(validationErr.Message, tt.path) {
						t.Error("error message should contain file path")
					}
				}
			} else {
				if result != nil || err != nil {
					t.Errorf("expected no error, got result=%v, err=%v", result, err)
				}
			}
		})
	}
}

func TestValidateTotalSize(t *testing.T) {
	tests := []struct {
		name      string
		totalSize int64
		expectErr bool
	}{
		{"small total", 1024 * 1024, false},
		{"at limit", MaxTotalPushSizeBytes, false},
		{"exceed limit", MaxTotalPushSizeBytes + 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ValidateTotalSize(tt.totalSize)
			if tt.expectErr {
				if result == nil && err == nil {
					t.Error("expected error, got nil")
				}
				validationErr, ok := err.(*ValidationError)
				if ok && validationErr.Code != "TOTAL_SIZE_TOO_LARGE" {
					t.Errorf("expected code TOTAL_SIZE_TOO_LARGE, got %s", validationErr.Code)
				}
			} else {
				if result != nil || err != nil {
					t.Errorf("expected no error, got result=%v, err=%v", result, err)
				}
			}
		})
	}
}

func TestValidateChunkSize(t *testing.T) {
	tests := []struct {
		name      string
		files     []FileEntry
		expectErr bool
	}{
		{
			name: "small chunk",
			files: []FileEntry{
				{Path: "file1.txt", Content: "content1"},
				{Path: "file2.txt", Content: "content2"},
			},
			expectErr: false,
		},
		{
			name: "chunk at limit",
			files: []FileEntry{
				{Path: "large.txt", Content: strings.Repeat("x", int(MaxTotalPushSizeBytes))},
			},
			expectErr: false,
		},
		{
			name: "chunk exceeds limit",
			files: []FileEntry{
				{Path: "too-large.txt", Content: strings.Repeat("x", int(MaxTotalPushSizeBytes+1))},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateChunkSize(tt.files)
			if tt.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				validationErr, ok := err.(*ValidationError)
				if ok && validationErr.Code != "CHUNK_TOO_LARGE" {
					t.Errorf("expected code CHUNK_TOO_LARGE, got %s", validationErr.Code)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestGetMaxChunkSize(t *testing.T) {
	maxChunkSize := GetMaxChunkSize()

	expectedSize := int64(float64(MaxTotalPushSizeBytes) * ChunkSafetyMarginPercent)
	if maxChunkSize != expectedSize {
		t.Errorf("expected max chunk size %d, got %d", expectedSize, maxChunkSize)
	}

	// Should be exactly 80MB (80% of 100MB)
	if maxChunkSize != 80*1024*1024 {
		t.Errorf("expected 80MB (83886080 bytes), got %d bytes", maxChunkSize)
	}
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1024 * 1024, "1.00 MB"},
		{1024 * 1024 * 1024, "1.00 GB"},
		{MaxFileSizeBytes, "25.00 MB"},
		{MaxTotalPushSizeBytes, "100.00 MB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := FormatFileSize(tt.bytes)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestValidationError_Error(t *testing.T) {
	tests := []struct {
		name       string
		err        *ValidationError
		expectsMsg string
	}{
		{
			name: "with suggestion",
			err: &ValidationError{
				Code:       "TEST_ERROR",
				Message:    "Something went wrong",
				Suggestion: "Try this fix",
			},
			expectsMsg: "Something went wrong. Suggestion: Try this fix",
		},
		{
			name: "without suggestion",
			err: &ValidationError{
				Code:    "TEST_ERROR",
				Message: "Something went wrong",
			},
			expectsMsg: "Something went wrong",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.expectsMsg {
				t.Errorf("expected %q, got %q", tt.expectsMsg, tt.err.Error())
			}
		})
	}
}

func BenchmarkValidateFiles(b *testing.B) {
	// Create a realistic set of files
	files := make([]interface{}, 100)
	for i := 0; i < 100; i++ {
		files[i] = map[string]interface{}{
			"path":    string(rune('a'+i%26)) + ".txt",
			"content": strings.Repeat("x", 10000),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = ValidateFiles(files)
	}
}
