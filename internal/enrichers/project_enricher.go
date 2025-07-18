package enrichers

import (
	"fmt"
	"strings"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
)

// ProjectEnricher handles project-specific enrichment logic
type ProjectEnricher struct{}

// NewProjectEnricher creates a new project enricher
func NewProjectEnricher() *ProjectEnricher {
	return &ProjectEnricher{}
}

// ObjectType returns the object type this enricher handles
func (e *ProjectEnricher) ObjectType() string {
	return constants.ObjectTypeProject
}

// EnrichData enriches project-specific data
func (e *ProjectEnricher) EnrichData(body *entities.TransactionBody, transaction *entities.LFXTransaction) error {
	data := transaction.ParsedData

	// Extract project ID
	if projectID, ok := data["id"].(string); ok {
		body.ObjectID = projectID
	} else {
		return fmt.Errorf("missing or invalid project ID")
	}

	// Extract project name for sorting
	if name, ok := data["name"].(string); ok {
		body.SortName = name
		body.NameAndAliases = []string{name}
	}

	// Extract slug as additional alias
	if slug, ok := data["slug"].(string); ok && slug != "" {
		body.NameAndAliases = append(body.NameAndAliases, slug)
	}

	// Set public flag
	if public, ok := data["public"].(bool); ok {
		body.Public = public
	}

	// Build fulltext search content
	var fulltext []string
	if body.SortName != "" {
		fulltext = append(fulltext, body.SortName)
	}
	for _, alias := range body.NameAndAliases {
		if alias != body.SortName {
			fulltext = append(fulltext, alias)
		}
	}
	body.Fulltext = strings.Join(fulltext, " ")

	return nil
}
