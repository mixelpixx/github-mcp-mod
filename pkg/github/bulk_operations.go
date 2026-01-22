package github

import (
	"context"
	"encoding/json"
	"fmt"

	ghErrors "github.com/github/github-mcp-server/pkg/errors"
	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/github/github-mcp-server/pkg/utils"
	"github.com/google/go-github/v79/github"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ChunkResult represents the result of processing a single chunk
type ChunkResult struct {
	ChunkIndex   int      `json:"chunk_index"`
	FilesInChunk int      `json:"files_in_chunk"`
	CommitSHA    string   `json:"commit_sha"`
	Success      bool     `json:"success"`
	Error        string   `json:"error,omitempty"`
	Files        []string `json:"files"`
}

// PushFilesChunkedResult represents the overall result of a chunked push operation
type PushFilesChunkedResult struct {
	TotalFiles       int           `json:"total_files"`
	TotalChunks      int           `json:"total_chunks"`
	SuccessfulChunks int           `json:"successful_chunks"`
	FailedChunks     int           `json:"failed_chunks"`
	FinalCommitSHA   string        `json:"final_commit_sha,omitempty"`
	Chunks           []ChunkResult `json:"chunks"`
	FullySuccessful  bool          `json:"fully_successful"`
}

// Deprecated: use FileEntry from validation.go instead
// Kept for backward compatibility with pushChunk signature
type fileEntry = FileEntry

// PushFilesChunked creates a tool to push multiple files in chunks, creating multiple commits.
// This is designed for large file operations that exceed the limits of push_files.
func PushFilesChunked(getClient GetClientFn, t translations.TranslationHelperFunc) (mcp.Tool, mcp.ToolHandlerFor[map[string]any, any]) {
	tool := mcp.Tool{
		Name:        "push_files_chunked",
		Description: t("TOOL_PUSH_FILES_CHUNKED_DESCRIPTION", "Push multiple files to a GitHub repository in chunks, creating multiple commits. Use this for large batches of files (>100 files) that exceed push_files limits."),
		Annotations: &mcp.ToolAnnotations{
			Title:        t("TOOL_PUSH_FILES_CHUNKED_USER_TITLE", "Push files in chunks"),
			ReadOnlyHint: false,
		},
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"owner": {
					Type:        "string",
					Description: "Repository owner",
				},
				"repo": {
					Type:        "string",
					Description: "Repository name",
				},
				"branch": {
					Type:        "string",
					Description: "Branch to push to",
				},
				"files": {
					Type:        "array",
					Description: "Array of file objects to push, each object with path (string) and content (string)",
					Items: &jsonschema.Schema{
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"path": {
								Type:        "string",
								Description: "path to the file",
							},
							"content": {
								Type:        "string",
								Description: "file content",
							},
						},
						Required: []string{"path", "content"},
					},
				},
				"message": {
					Type:        "string",
					Description: "Base commit message (chunk number will be appended)",
				},
				"chunk_size": {
					Type:        "integer",
					Description: fmt.Sprintf("Number of files per chunk (default: %d, max: %d)", DefaultChunkSize, MaxChunkSize),
					Default:     json.RawMessage(fmt.Sprintf("%d", DefaultChunkSize)),
				},
				"continue_on_error": {
					Type:        "boolean",
					Description: "Continue processing remaining chunks if one fails (default: false)",
					Default:     json.RawMessage("false"),
				},
			},
			Required: []string{"owner", "repo", "branch", "files", "message"},
		},
	}

	handler := mcp.ToolHandlerFor[map[string]any, any](func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		owner, err := RequiredParam[string](args, "owner")
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		repo, err := RequiredParam[string](args, "repo")
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		branch, err := RequiredParam[string](args, "branch")
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		message, err := RequiredParam[string](args, "message")
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}

		chunkSize, err := OptionalIntParamWithDefault(args, "chunk_size", DefaultChunkSize)
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		if chunkSize > MaxChunkSize {
			chunkSize = MaxChunkSize
		}
		if chunkSize < 1 {
			chunkSize = 1
		}

		continueOnError, err := OptionalParam[bool](args, "continue_on_error")
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}

		filesObj, ok := args["files"].([]interface{})
		if !ok {
			return utils.NewToolResultError("files parameter must be an array of objects with path and content"), nil, nil
		}

		if len(filesObj) == 0 {
			return utils.NewToolResultError("files array cannot be empty"), nil, nil
		}

		// Validate all files using shared validation logic
		validationResult, files, err := ValidateFiles(filesObj)
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}

		// Check for oversized files
		for _, path := range validationResult.OversizedFiles {
			if result, err := ValidateFileSize(path, validationResult.LargestFileSize); result != nil || err != nil {
				return result, nil, nil
			}
		}

		client, err := getClient(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get GitHub client: %w", err)
		}

		// Create size-aware chunks using safety margin
		maxChunkBytes := GetMaxChunkSize()
		var chunks [][]FileEntry

		var currentChunk []fileEntry
		var currentChunkSize int64
		var currentChunkFileCount int

		for _, file := range files {
			fileSize := int64(len(file.Content))

			// Check if adding this file would exceed limits
			wouldExceedSize := currentChunkSize+fileSize > maxChunkBytes
			wouldExceedCount := currentChunkFileCount >= chunkSize

			// Start a new chunk if we'd exceed either limit (and current chunk is not empty)
			if len(currentChunk) > 0 && (wouldExceedSize || wouldExceedCount) {
				chunks = append(chunks, currentChunk)
				currentChunk = []fileEntry{}
				currentChunkSize = 0
				currentChunkFileCount = 0
			}

			currentChunk = append(currentChunk, file)
			currentChunkSize += fileSize
			currentChunkFileCount++
		}

		// Add the last chunk if it has files
		if len(currentChunk) > 0 {
			chunks = append(chunks, currentChunk)
		}

		result := PushFilesChunkedResult{
			TotalFiles:  len(files),
			TotalChunks: len(chunks),
			Chunks:      make([]ChunkResult, 0, len(chunks)),
		}

		// Process each chunk
		for chunkIdx, chunkFiles := range chunks {
			chunkResult := ChunkResult{
				ChunkIndex:   chunkIdx + 1,
				FilesInChunk: len(chunkFiles),
				Files:        make([]string, 0, len(chunkFiles)),
			}

			for _, f := range chunkFiles {
				chunkResult.Files = append(chunkResult.Files, f.Path)
			}

			// Generate commit message for this chunk
			chunkMessage := message
			if result.TotalChunks > 1 {
				chunkMessage = fmt.Sprintf("%s [chunk %d/%d]", message, chunkIdx+1, result.TotalChunks)
			}

			// Push this chunk
			commitSHA, pushErr := pushChunk(ctx, client, owner, repo, branch, chunkFiles, chunkMessage)
			if pushErr != nil {
				chunkResult.Success = false
				chunkResult.Error = pushErr.Error()
				result.FailedChunks++

				if !continueOnError {
					result.Chunks = append(result.Chunks, chunkResult)
					result.FullySuccessful = false

					r, _ := json.Marshal(result)
					return utils.NewToolResultText(string(r)), nil, nil
				}
			} else {
				chunkResult.Success = true
				chunkResult.CommitSHA = commitSHA
				result.SuccessfulChunks++
				result.FinalCommitSHA = commitSHA
			}

			result.Chunks = append(result.Chunks, chunkResult)
		}

		result.FullySuccessful = result.FailedChunks == 0

		r, err := json.Marshal(result)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal response: %w", err)
		}

		return utils.NewToolResultText(string(r)), nil, nil
	})

	return tool, handler
}

// pushChunk pushes a single chunk of files to the repository
func pushChunk(ctx context.Context, client *github.Client, owner, repo, branch string, files []FileEntry, message string) (string, error) {
	// Validate chunk size before attempting to push
	if err := ValidateChunkSize(files); err != nil {
		return "", err
	}

	// Get the reference for the branch
	ref, resp, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
	if err != nil {
		_, apiErr := ghErrors.NewGitHubAPIErrorToCtx(ctx, "failed to get branch reference", resp, err)
		return "", apiErr
	}
	defer func() { _ = resp.Body.Close() }()

	// Get the commit object that the branch points to
	baseCommit, resp, err := client.Git.GetCommit(ctx, owner, repo, *ref.Object.SHA)
	if err != nil {
		_, apiErr := ghErrors.NewGitHubAPIErrorToCtx(ctx, "failed to get base commit", resp, err)
		return "", apiErr
	}
	defer func() { _ = resp.Body.Close() }()

	// Create tree entries for all files in this chunk
	var entries []*github.TreeEntry
	for _, file := range files {
		entries = append(entries, &github.TreeEntry{
			Path:    github.Ptr(file.Path),
			Mode:    github.Ptr("100644"),
			Type:    github.Ptr("blob"),
			Content: github.Ptr(file.Content),
		})
	}

	// Create a new tree
	newTree, resp, err := client.Git.CreateTree(ctx, owner, repo, *baseCommit.Tree.SHA, entries)
	if err != nil {
		_, apiErr := ghErrors.NewGitHubAPIErrorToCtx(ctx, "failed to create tree", resp, err)
		return "", apiErr
	}
	defer func() { _ = resp.Body.Close() }()

	// Create a new commit
	commit := github.Commit{
		Message: github.Ptr(message),
		Tree:    newTree,
		Parents: []*github.Commit{{SHA: baseCommit.SHA}},
	}
	newCommit, resp, err := client.Git.CreateCommit(ctx, owner, repo, commit, nil)
	if err != nil {
		_, apiErr := ghErrors.NewGitHubAPIErrorToCtx(ctx, "failed to create commit", resp, err)
		return "", apiErr
	}
	defer func() { _ = resp.Body.Close() }()

	// Update the reference to point to the new commit
	_, resp, err = client.Git.UpdateRef(ctx, owner, repo, *ref.Ref, github.UpdateRef{
		SHA:   *newCommit.SHA,
		Force: github.Ptr(false),
	})
	if err != nil {
		_, apiErr := ghErrors.NewGitHubAPIErrorToCtx(ctx, "failed to update reference", resp, err)
		return "", apiErr
	}
	defer func() { _ = resp.Body.Close() }()

	return *newCommit.SHA, nil
}

// GetPushLimits creates a tool to get the current push operation limits
func GetPushLimits(t translations.TranslationHelperFunc) (mcp.Tool, mcp.ToolHandlerFor[map[string]any, any]) {
	tool := mcp.Tool{
		Name:        "get_push_limits",
		Description: t("TOOL_GET_PUSH_LIMITS_DESCRIPTION", "Get the current limits for file push operations"),
		Annotations: &mcp.ToolAnnotations{
			Title:        t("TOOL_GET_PUSH_LIMITS_USER_TITLE", "Get push limits"),
			ReadOnlyHint: true,
		},
		InputSchema: &jsonschema.Schema{
			Type:       "object",
			Properties: map[string]*jsonschema.Schema{},
		},
	}

	handler := mcp.ToolHandlerFor[map[string]any, any](func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		limits := map[string]interface{}{
			"max_files_per_push":        MaxFilesPerPush,
			"max_file_size_bytes":       MaxFileSizeBytes,
			"max_file_size_mb":          MaxFileSizeBytes / (1024 * 1024),
			"max_total_push_size_bytes": MaxTotalPushSizeBytes,
			"max_total_push_size_mb":    MaxTotalPushSizeBytes / (1024 * 1024),
			"default_chunk_size":        DefaultChunkSize,
			"max_chunk_size":            MaxChunkSize,
			"recommendations": map[string]string{
				"small_batch":  "Use push_files for <= 100 files",
				"large_batch":  "Use push_files_chunked for > 100 files",
				"single_file":  "Use create_or_update_file for single files",
			},
		}

		r, err := json.Marshal(limits)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal response: %w", err)
		}

		return utils.NewToolResultText(string(r)), nil, nil
	})

	return tool, handler
}

// BulkDeleteFiles creates a tool to delete multiple files in a single commit
func BulkDeleteFiles(getClient GetClientFn, t translations.TranslationHelperFunc) (mcp.Tool, mcp.ToolHandlerFor[map[string]any, any]) {
	tool := mcp.Tool{
		Name:        "bulk_delete_files",
		Description: t("TOOL_BULK_DELETE_FILES_DESCRIPTION", "Delete multiple files from a GitHub repository in a single commit"),
		Annotations: &mcp.ToolAnnotations{
			Title:        t("TOOL_BULK_DELETE_FILES_USER_TITLE", "Bulk delete files"),
			ReadOnlyHint: false,
		},
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"owner": {
					Type:        "string",
					Description: "Repository owner",
				},
				"repo": {
					Type:        "string",
					Description: "Repository name",
				},
				"branch": {
					Type:        "string",
					Description: "Branch to delete files from",
				},
				"paths": {
					Type:        "array",
					Description: "Array of file paths to delete",
					Items: &jsonschema.Schema{
						Type: "string",
					},
				},
				"message": {
					Type:        "string",
					Description: "Commit message",
				},
			},
			Required: []string{"owner", "repo", "branch", "paths", "message"},
		},
	}

	handler := mcp.ToolHandlerFor[map[string]any, any](func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		owner, err := RequiredParam[string](args, "owner")
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		repo, err := RequiredParam[string](args, "repo")
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		branch, err := RequiredParam[string](args, "branch")
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		message, err := RequiredParam[string](args, "message")
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}

		pathsObj, ok := args["paths"].([]interface{})
		if !ok {
			return utils.NewToolResultError("paths parameter must be an array of strings"), nil, nil
		}

		if len(pathsObj) == 0 {
			return utils.NewToolResultError("paths array cannot be empty"), nil, nil
		}

		if len(pathsObj) > MaxFilesPerPush {
			return utils.NewToolResultError(fmt.Sprintf(
				"too many files to delete: %d exceeds maximum of %d per operation",
				len(pathsObj), MaxFilesPerPush,
			)), nil, nil
		}

		var paths []string
		for i, p := range pathsObj {
			path, ok := p.(string)
			if !ok || path == "" {
				return utils.NewToolResultError(fmt.Sprintf("path at index %d must be a non-empty string", i)), nil, nil
			}
			paths = append(paths, path)
		}

		client, err := getClient(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get GitHub client: %w", err)
		}

		// Get the reference for the branch
		ref, resp, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
		if err != nil {
			return ghErrors.NewGitHubAPIErrorResponse(ctx, "failed to get branch reference", resp, err), nil, nil
		}
		defer func() { _ = resp.Body.Close() }()

		// Get the commit object
		baseCommit, resp, err := client.Git.GetCommit(ctx, owner, repo, *ref.Object.SHA)
		if err != nil {
			return ghErrors.NewGitHubAPIErrorResponse(ctx, "failed to get base commit", resp, err), nil, nil
		}
		defer func() { _ = resp.Body.Close() }()

		// Create tree entries for deletion (SHA nil = delete)
		var entries []*github.TreeEntry
		for _, path := range paths {
			entries = append(entries, &github.TreeEntry{
				Path: github.Ptr(path),
				Mode: github.Ptr("100644"),
				Type: github.Ptr("blob"),
				SHA:  nil, // nil SHA means delete
			})
		}

		// Create new tree
		newTree, resp, err := client.Git.CreateTree(ctx, owner, repo, *baseCommit.Tree.SHA, entries)
		if err != nil {
			return ghErrors.NewGitHubAPIErrorResponse(ctx, "failed to create tree", resp, err), nil, nil
		}
		defer func() { _ = resp.Body.Close() }()

		// Create commit
		commit := github.Commit{
			Message: github.Ptr(message),
			Tree:    newTree,
			Parents: []*github.Commit{{SHA: baseCommit.SHA}},
		}
		newCommit, resp, err := client.Git.CreateCommit(ctx, owner, repo, commit, nil)
		if err != nil {
			return ghErrors.NewGitHubAPIErrorResponse(ctx, "failed to create commit", resp, err), nil, nil
		}
		defer func() { _ = resp.Body.Close() }()

		// Update reference
		updatedRef, resp, err := client.Git.UpdateRef(ctx, owner, repo, *ref.Ref, github.UpdateRef{
			SHA:   *newCommit.SHA,
			Force: github.Ptr(false),
		})
		if err != nil {
			return ghErrors.NewGitHubAPIErrorResponse(ctx, "failed to update reference", resp, err), nil, nil
		}
		defer func() { _ = resp.Body.Close() }()

		result := map[string]interface{}{
			"commit_sha":    *newCommit.SHA,
			"deleted_files": paths,
			"files_deleted": len(paths),
			"ref":           *updatedRef.Ref,
		}

		r, err := json.Marshal(result)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal response: %w", err)
		}

		return utils.NewToolResultText(string(r)), nil, nil
	})

	return tool, handler
}
