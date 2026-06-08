package handler

import "encoding/json"

type outlineItemPublicJSON struct {
	ID               string                  `json:"id,omitempty"`
	Kind             string                  `json:"kind"`
	Name             string                  `json:"name"`
	Detail           string                  `json:"detail,omitempty"`
	Path             []string                `json:"path,omitempty"`
	EnclosingPath    []string                `json:"enclosing_path,omitempty"`
	Range            SourceLineRange         `json:"range"`
	ByteRange        *SourceByteRange        `json:"byte_range,omitempty"`
	Depth            int                     `json:"depth,omitempty"`
	Confidence       string                  `json:"confidence,omitempty"`
	RangeIsEstimated *bool                   `json:"range_is_estimated,omitempty"`
	RangeFingerprint *FileFingerprint        `json:"range_fingerprint,omitempty"`
	Selector         *OutlineSelector        `json:"selector,omitempty"`
	SymbolRef        string                  `json:"symbol_ref,omitempty"`
	WholeLineRange   *bool                   `json:"whole_line_range,omitempty"`
	WriteSafe        *bool                   `json:"write_safe,omitempty"`
	RefusalReason    string                  `json:"refusal_reason,omitempty"`
	Children         []outlineItemPublicJSON `json:"children,omitempty"`
	Metadata         map[string]string       `json:"metadata,omitempty"`
}

func projectOutlineOutput(output OutlineFileOutput, options outlineOptions) OutlineFileOutput {
	output.publicOutputProfile = options.outputProfile
	output.publicIncludeWriteMetadata = options.includeWriteMetadata
	return output
}

func (output OutlineFileOutput) MarshalJSON() ([]byte, error) {
	type outlineFileOutputJSON OutlineFileOutput
	projected := outlineFileOutputJSON(output)
	if output.publicOutputProfile != outlineProfileFull {
		projected.Imports = projectOutlineItems(projected.Imports, output.publicIncludeWriteMetadata)
		projected.Symbols = projectOutlineItems(projected.Symbols, output.publicIncludeWriteMetadata)
		projected.Sections = projectOutlineItems(projected.Sections, output.publicIncludeWriteMetadata)
		projected.EnclosingItems = projectOutlineItems(projected.EnclosingItems, output.publicIncludeWriteMetadata)
	}
	type outlineFilePublicJSON struct {
		CwdOutputMeta
		Text                string                  `json:"-"`
		Error               string                  `json:"error,omitempty"`
		File                string                  `json:"file,omitempty"`
		Language            string                  `json:"language,omitempty"`
		ParserStatus        string                  `json:"parser_status,omitempty"`
		ParserScope         string                  `json:"parser_scope,omitempty"`
		Fingerprint         *FileFingerprint        `json:"fingerprint,omitempty"`
		Imports             []outlineItemPublicJSON `json:"imports"`
		Symbols             []outlineItemPublicJSON `json:"symbols"`
		Sections            []outlineItemPublicJSON `json:"sections"`
		EnclosingItems      []outlineItemPublicJSON `json:"enclosing_items,omitempty"`
		OutlineStats        OutlineStats            `json:"outline_stats"`
		Truncated           bool                    `json:"truncated"`
		Warnings            []ToolWarning           `json:"warnings"`
		NextRecommendedCall *ActionHint             `json:"next_recommended_call,omitempty"`
		ErrorCode           string                  `json:"error_code,omitempty"`
	}
	public := outlineFilePublicJSON{
		CwdOutputMeta:       projected.CwdOutputMeta,
		Text:                projected.Text,
		Error:               projected.Error,
		File:                projected.File,
		Language:            projected.Language,
		ParserStatus:        projected.ParserStatus,
		ParserScope:         projected.ParserScope,
		Fingerprint:         projected.Fingerprint,
		Imports:             outlineItemsPublicJSON(projected.Imports, output.publicOutputProfile == outlineProfileFull),
		Symbols:             outlineItemsPublicJSON(projected.Symbols, output.publicOutputProfile == outlineProfileFull),
		Sections:            outlineItemsPublicJSON(projected.Sections, output.publicOutputProfile == outlineProfileFull),
		EnclosingItems:      outlineItemsPublicJSON(projected.EnclosingItems, output.publicOutputProfile == outlineProfileFull),
		OutlineStats:        projected.OutlineStats,
		Truncated:           projected.Truncated,
		Warnings:            projected.Warnings,
		NextRecommendedCall: projected.NextRecommendedCall,
		ErrorCode:           projected.ErrorCode,
	}
	return json.Marshal(public)
}

func outlineItemsPublicJSON(items []OutlineItem, includeDefaultTrustFields bool) []outlineItemPublicJSON {
	if len(items) == 0 {
		return []outlineItemPublicJSON{}
	}
	out := make([]outlineItemPublicJSON, len(items))
	for i, item := range items {
		out[i] = outlineItemPublicJSON{
			ID:               item.ID,
			Kind:             item.Kind,
			Name:             item.Name,
			Detail:           item.Detail,
			Path:             item.Path,
			EnclosingPath:    item.EnclosingPath,
			Range:            item.Range,
			ByteRange:        item.ByteRange,
			Depth:            item.Depth,
			Confidence:       item.Confidence,
			RangeFingerprint: item.RangeFingerprint,
			Selector:         item.Selector,
			SymbolRef:        item.SymbolRef,
			WholeLineRange:   item.WholeLineRange,
			WriteSafe:        item.WriteSafe,
			RefusalReason:    item.RefusalReason,
			Children:         outlineItemsPublicJSON(item.Children, includeDefaultTrustFields),
			Metadata:         item.Metadata,
		}
		if includeDefaultTrustFields || item.RangeIsEstimated {
			value := item.RangeIsEstimated
			out[i].RangeIsEstimated = &value
		}
	}
	return out
}

func projectOutlineOutputStruct(output OutlineFileOutput, options outlineOptions) OutlineFileOutput {
	if options.outputProfile == outlineProfileFull {
		return output
	}
	output.Imports = projectOutlineItems(output.Imports, options.includeWriteMetadata)
	output.Symbols = projectOutlineItems(output.Symbols, options.includeWriteMetadata)
	output.Sections = projectOutlineItems(output.Sections, options.includeWriteMetadata)
	output.EnclosingItems = projectOutlineItems(output.EnclosingItems, options.includeWriteMetadata)
	return output
}

func projectOutlineItems(items []OutlineItem, includeWriteMetadata bool) []OutlineItem {
	if len(items) == 0 {
		return items
	}
	out := make([]OutlineItem, len(items))
	for i, item := range items {
		item.Children = projectOutlineItems(item.Children, includeWriteMetadata)
		out[i] = projectOutlineItem(item, includeWriteMetadata)
	}
	return out
}

func projectOutlineItem(item OutlineItem, includeWriteMetadata bool) OutlineItem {
	projected := item
	projected.ID = ""
	projected.Metadata = nil
	if projected.Confidence == "exact" && !projected.RangeIsEstimated {
		projected.Confidence = ""
	}
	if !includeWriteMetadata {
		projected.ByteRange = nil
		projected.RangeFingerprint = nil
		projected.Selector = nil
		projected.WholeLineRange = nil
		projected.WriteSafe = nil
		projected.RefusalReason = ""
	}
	return projected
}
