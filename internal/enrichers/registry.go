package enrichers

import (
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
)

// Enricher defines the interface for object-specific enrichment logic
type Enricher interface {
	// EnrichData enriches the transaction body with object-specific data
	EnrichData(body *entities.TransactionBody, transaction *entities.LFXTransaction) error

	// ObjectType returns the object type this enricher handles
	ObjectType() string
}

// Registry is a simple registry for object enrichers
// No complex dependency injection or configuration - just a map
type Registry struct {
	enrichers map[string]Enricher
}

// NewRegistry creates a new enricher registry
func NewRegistry() *Registry {
	return &Registry{
		enrichers: make(map[string]Enricher),
	}
}

// Register registers an enricher for a specific object type
func (r *Registry) Register(enricher Enricher) {
	r.enrichers[enricher.ObjectType()] = enricher
}

// GetEnricher returns the enricher for a specific object type
func (r *Registry) GetEnricher(objectType string) (Enricher, bool) {
	enricher, exists := r.enrichers[objectType]
	return enricher, exists
}

// GetSupportedTypes returns all supported object types
func (r *Registry) GetSupportedTypes() []string {
	types := make([]string, 0, len(r.enrichers))
	for objectType := range r.enrichers {
		types = append(types, objectType)
	}
	return types
}
