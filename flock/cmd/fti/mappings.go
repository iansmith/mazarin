package main

import (
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"

	"mazzy/shared/fti"
)

// bleveMailDoc is the document structure indexed by bleve for mail messages.
// Field names must match the mapping field names.
type bleveMailDoc struct {
	Subject string    `json:"subject"`
	From    string    `json:"from"`
	Sender  string    `json:"sender"`
	Body    string    `json:"body"`
	Date    time.Time `json:"date"`
}

// buildIndexMapping creates a bleve IndexMapping with per-type document
// mappings. Each IndexType constant maps to a named document mapping
// with appropriate analyzers and boost values.
func buildIndexMapping() *mapping.IndexMappingImpl {
	indexMapping := bleve.NewIndexMapping()

	// Mail message mapping.
	mailMapping := bleve.NewDocumentMapping()

	// Subject: standard text, boosted for search ranking.
	subjectField := bleve.NewTextFieldMapping()
	subjectField.Analyzer = "standard"
	subjectField.Store = true
	subjectField.IncludeInAll = true
	mailMapping.AddFieldMappingsAt("subject", subjectField)

	// From: keyword analyzer (exact match, no stemming).
	fromField := bleve.NewTextFieldMapping()
	fromField.Analyzer = "keyword"
	fromField.Store = true
	mailMapping.AddFieldMappingsAt("from", fromField)

	// Sender: keyword analyzer.
	senderField := bleve.NewTextFieldMapping()
	senderField.Analyzer = "keyword"
	senderField.Store = true
	mailMapping.AddFieldMappingsAt("sender", senderField)

	// Body: standard text, NOT stored (too large).
	bodyField := bleve.NewTextFieldMapping()
	bodyField.Analyzer = "standard"
	bodyField.Store = false
	bodyField.IncludeInAll = true
	mailMapping.AddFieldMappingsAt("body", bodyField)

	// Date: datetime for range queries.
	dateField := bleve.NewDateTimeFieldMapping()
	mailMapping.AddFieldMappingsAt("date", dateField)

	// Register under the type name used by indexHandler.
	indexMapping.AddDocumentMapping(docTypeName(fti.IndexTypeMailMessage), mailMapping)

	// Disable dynamic mapping on the default to avoid indexing unexpected fields.
	indexMapping.DefaultMapping.Dynamic = false

	return indexMapping
}

// docTypeName returns the bleve document type name for an IndexType constant.
func docTypeName(t uint32) string {
	switch t {
	case fti.IndexTypeMailMessage:
		return "mail_message"
	case fti.IndexTypeCalendarEvent:
		return "calendar_event"
	case fti.IndexTypeReminder:
		return "reminder"
	default:
		return "unknown"
	}
}
