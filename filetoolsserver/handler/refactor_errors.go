package handler

import "strings"

func errorCodeFromMessage(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "batch_limit_exceeded"):
		return "batch_limit_exceeded"
	case strings.Contains(lower, "vcs_content_traversal_unsupported"):
		return "vcs_content_traversal_unsupported"
	case strings.Contains(lower, "invalid_continuation_proof"):
		return "invalid_continuation_proof"
	case strings.Contains(lower, "continuation_stale"):
		return "continuation_stale"
	case strings.Contains(lower, "continuation_query_mismatch"):
		return "continuation_query_mismatch"
	case strings.Contains(lower, "too_many_items"):
		return "too_many_items"
	case strings.Contains(lower, "invalid_read_range"):
		return "invalid_read_range"
	case strings.Contains(lower, "invalid_redaction_mode"):
		return "invalid_redaction_mode"
	case strings.Contains(lower, "invalid_joiner") || strings.Contains(lower, "unsupported joiner"):
		return "invalid_joiner"
	case strings.Contains(lower, "invalid_placement") || strings.Contains(lower, "unsupported placement") || strings.Contains(lower, "placement."):
		return "invalid_placement"
	case strings.Contains(lower, "invalid_backup_mode") || strings.Contains(lower, "unsupported backup.mode") || strings.Contains(lower, "unsupported source_backup.mode"):
		return "invalid_backup_mode"
	case strings.Contains(lower, "backup_creation_failed"):
		return "backup_creation_failed"
	case strings.Contains(lower, "post_write_validation_failed"):
		return "post_write_validation_failed"
	case strings.Contains(lower, "target_post_write_inspect_failed"):
		return "target_post_write_inspect_failed"
	case strings.Contains(lower, "source_post_write_inspect_failed"):
		return "source_post_write_inspect_failed"
	case strings.Contains(lower, "zero_byte_range"):
		return "zero_byte_range"
	case strings.Contains(lower, "source_fingerprint_mismatch"):
		return "source_fingerprint_mismatch"
	case strings.Contains(lower, "target_fingerprint_mismatch"):
		return "target_fingerprint_mismatch"
	case strings.Contains(lower, "ranges must be non-overlapping"):
		return "overlapping_ranges"
	case strings.Contains(lower, "out of bounds"):
		return "range_out_of_bounds"
	case strings.Contains(lower, "target_exists"):
		return "target_exists"
	case strings.Contains(lower, "target_missing"):
		return "target_missing"
	case strings.Contains(lower, "must not refer to the same file") || strings.Contains(lower, "target files must be unique"):
		return "same_file_operation_unsupported"
	case strings.Contains(lower, "binary files are not supported"):
		return "binary_file_rejected"
	case strings.Contains(lower, "unsupported encoding") || strings.Contains(lower, "only utf-8/ascii text writes are supported"):
		return "unsupported_encoding"
	case strings.Contains(lower, "parent_directory_missing") || strings.Contains(lower, "target parent is not a directory") || strings.Contains(lower, "parent directory"):
		return "parent_directory_missing"
	case strings.Contains(lower, "must be relative when cwd_id"):
		return "absolute_path_not_allowed_with_cwd"
	case strings.Contains(lower, "escapes cwd_id") || strings.Contains(lower, "outside cwd_id") || strings.Contains(lower, "resolves outside cwd"):
		return "path_outside_cwd"
	case strings.Contains(lower, "relative paths require cwd_id") || strings.Contains(lower, "must be an absolute path"):
		return "relative_path_requires_cwd"
	case strings.Contains(lower, "is required and must be"):
		return "invalid_path"
	case strings.Contains(lower, "backup"):
		return "backup_failed"
	case strings.Contains(lower, "threshold"):
		return "threshold_exceeded"
	case strings.Contains(lower, "absolute path"):
		return "invalid_path"
	case strings.Contains(lower, "symlink"):
		return "symlink_rejected"
	default:
		return "refactor_error"
	}
}

func actionHintForTransferError(code, sourceFile, targetFile string) *ActionHint {
	hint := &ActionHint{SafeToRetry: false}
	switch code {
	case "source_fingerprint_mismatch", "source_post_write_inspect_failed", "zero_byte_range", "range_out_of_bounds":
		hint.RecommendedNextTool = "outline_file"
		hint.RecommendedNextInput = map[string]any{
			"target_file":    sourceFile,
			"output_profile": "outline",
		}
		if code == "source_post_write_inspect_failed" {
			hint.RecommendedNextInput["output_profile"] = "fingerprint_only"
			hint.Reason = "Source was written but its fingerprint could not be confirmed; inspect the source fingerprint before retrying."
		} else if code == "zero_byte_range" {
			hint.Reason = "Refresh the source outline and select a range that maps to actual bytes."
		} else {
			hint.Reason = "Refresh the source outline/fingerprint before retrying."
		}
	case "target_fingerprint_mismatch", "target_post_write_inspect_failed", "target_missing", "target_exists":
		if code == "target_missing" {
			hint.RecommendedNextTool = "inspect_path"
			hint.RecommendedNextInput = map[string]any{
				"target_path": targetFile,
			}
			hint.Reason = "Target file is missing; inspect the path, then create it first or retry with placement=create_new and target_precondition.must_not_exist=true."
		} else {
			hint.RecommendedNextTool = "outline_file"
			hint.RecommendedNextInput = map[string]any{
				"target_file":    targetFile,
				"output_profile": "fingerprint_only",
			}
			if code == "target_post_write_inspect_failed" {
				hint.Reason = "Target was written but its fingerprint could not be confirmed; inspect the target fingerprint before retrying."
			} else {
				hint.Reason = "Refresh the target precondition before retrying."
			}
		}
	case "overlapping_ranges", "same_file_operation_unsupported", "binary_file_rejected", "unsupported_encoding", "parent_directory_missing":
		hint.Reason = "Change the input or selected file before retrying."
	case "backup_failed", "threshold_exceeded", "invalid_path", "symlink_rejected":
		hint.Reason = "The input or filesystem state must change before retrying."
	case "absolute_path_not_allowed_with_cwd":
		hint.Reason = "Omit cwd_id for absolute paths, or pass a relative path under the registered cwd."
	case "path_outside_cwd":
		hint.Reason = "Use a path inside the registered cwd, or register a different cwd with set_cwd."
	case "relative_path_requires_cwd":
		hint.Reason = "Call set_cwd and pass cwd_id for relative paths, or use an absolute path without cwd_id."
	default:
		hint.Reason = "Inspect structured error fields before retrying."
	}
	return hint
}
