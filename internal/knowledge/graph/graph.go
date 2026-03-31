package graph

import (
	"context"
	"time"
)

// Node represents an entity in the knowledge graph.
type Node struct {
	ID         string
	Type       string
	Name       string
	Properties map[string]string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Edge represents a relationship between two nodes.
type Edge struct {
	ID         string
	SourceID   string
	TargetID   string
	Type       string
	Weight     float64
	Properties map[string]string
	CreatedAt  time.Time
	ValidFrom  *time.Time `json:"valid_from,omitempty"`
	ValidTo    *time.Time `json:"valid_to,omitempty"`
}

// Triple is a subject-predicate-object relation.
type Triple struct {
	Subject   Node
	Predicate string
	Object    Node
	Weight    float64
}

// Graph is the interface for knowledge graph operations.
type Graph interface {
	// UpsertNode creates or updates a node. Returns the node ID.
	UpsertNode(ctx context.Context, node Node) (string, error)
	// UpsertEdge creates or updates an edge. Returns the edge ID.
	UpsertEdge(ctx context.Context, edge Edge) (string, error)
	// Neighbors returns all nodes directly connected to nodeID.
	Neighbors(ctx context.Context, nodeID string, edgeType string) ([]Triple, error)
	// Traverse performs multi-hop traversal from nodeID up to maxDepth.
	Traverse(ctx context.Context, nodeID string, maxDepth int) ([]Triple, error)
	// FindNode finds a node by type and name.
	FindNode(ctx context.Context, nodeType, name string) (*Node, error)
	// FindByName fuzzy-matches nodes by name substring.
	FindByName(ctx context.Context, name string) ([]Node, error)
	// AddProvenance links an edge to its source.
	AddProvenance(ctx context.Context, edgeID, sourceType, sourceID string) error
}
