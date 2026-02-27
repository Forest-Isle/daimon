package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// EntityCompleter is a minimal LLM interface for entity extraction.
type EntityCompleter interface {
	Complete(ctx context.Context, systemPrompt, userMessage string) (string, error)
}

// RawTriple is the JSON shape returned by the LLM.
type RawTriple struct {
	Subject     string `json:"subject"`
	SubjectType string `json:"subject_type"`
	Predicate   string `json:"predicate"`
	Object      string `json:"object"`
	ObjectType  string `json:"object_type"`
}

// LLMEntityExtractor extracts entities and relations from text using an LLM.
type LLMEntityExtractor struct {
	graph     Graph
	completer EntityCompleter
}

// NewLLMEntityExtractor creates a new extractor.
func NewLLMEntityExtractor(g Graph, completer EntityCompleter) *LLMEntityExtractor {
	return &LLMEntityExtractor{graph: g, completer: completer}
}

const entityExtractionPrompt = `You are an entity and relation extractor. Extract named entities and their relationships from the given text.

Output ONLY a JSON array of triples:
[{"subject": "<name>", "subject_type": "<person|org|concept|location|product>", "predicate": "<relationship>", "object": "<name>", "object_type": "<person|org|concept|location|product>"}]

Rules:
- Only extract clear, factual relationships
- subject_type and object_type must be one of: person, org, concept, location, product
- predicate should be a short verb phrase: "works_at", "knows", "located_in", "part_of", "related_to"
- Maximum 10 triples
- If no clear entities/relations found, output: []`

// Extract processes text and writes entities/relations to the graph.
// sourceType and sourceID are for provenance tracking.
func (e *LLMEntityExtractor) Extract(ctx context.Context, text, sourceType, sourceID string) error {
	if len(text) > 3000 {
		text = text[:3000]
	}

	resp, err := e.completer.Complete(ctx, entityExtractionPrompt, text)
	if err != nil {
		return fmt.Errorf("entity extraction LLM call: %w", err)
	}

	rawTriples, err := parseRawTriples(resp)
	if err != nil || len(rawTriples) == 0 {
		return nil
	}

	for _, rt := range rawTriples {
		if rt.Subject == "" || rt.Object == "" || rt.Predicate == "" {
			continue
		}

		// Upsert subject node
		subjectID, err := e.graph.UpsertNode(ctx, Node{
			ID:   fmt.Sprintf("node_%d", time.Now().UnixNano()),
			Type: normalizeType(rt.SubjectType),
			Name: rt.Subject,
		})
		if err != nil {
			slog.Warn("graph: upsert subject node failed", "err", err)
			continue
		}

		// Upsert object node
		objectID, err := e.graph.UpsertNode(ctx, Node{
			ID:   fmt.Sprintf("node_%d", time.Now().UnixNano()),
			Type: normalizeType(rt.ObjectType),
			Name: rt.Object,
		})
		if err != nil {
			slog.Warn("graph: upsert object node failed", "err", err)
			continue
		}

		// Upsert edge
		edgeID, err := e.graph.UpsertEdge(ctx, Edge{
			ID:       fmt.Sprintf("edge_%d", time.Now().UnixNano()),
			SourceID: subjectID,
			TargetID: objectID,
			Type:     rt.Predicate,
			Weight:   1.0,
		})
		if err != nil {
			slog.Warn("graph: upsert edge failed", "err", err)
			continue
		}

		// Record provenance
		if sourceID != "" {
			if err := e.graph.AddProvenance(ctx, edgeID, sourceType, sourceID); err != nil {
				slog.Warn("graph: add provenance failed", "err", err)
			}
		}
	}
	return nil
}

func parseRawTriples(text string) ([]RawTriple, error) {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return nil, nil
	}
	var triples []RawTriple
	if err := json.Unmarshal([]byte(text[start:end+1]), &triples); err != nil {
		return nil, err
	}
	return triples, nil
}

func normalizeType(t string) string {
	switch strings.ToLower(t) {
	case "person":
		return "person"
	case "org", "organization", "company":
		return "org"
	case "location", "place", "city", "country":
		return "location"
	case "product":
		return "product"
	default:
		return "concept"
	}
}
