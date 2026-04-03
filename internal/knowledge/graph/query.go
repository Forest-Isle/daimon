package graph

import (
	"context"
	"fmt"
	"strings"
)

// QueryResult is a structured result from a graph query.
type QueryResult struct {
	NodeID   string
	NodeType string
	NodeName string
	Path     []string // sequence of node names from root
	Depth    int
}

// DescribeNode returns a human-readable description of a node and its neighbors.
func DescribeNode(ctx context.Context, g Graph, nodeType, name string) (string, error) {
	node, err := g.FindNode(ctx, nodeType, name)
	if err != nil {
		return "", fmt.Errorf("node not found: %s/%s", nodeType, name)
	}

	triples, err := g.Neighbors(ctx, node.ID, "")
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "%s (%s)", node.Name, node.Type)
	if len(triples) > 0 {
		sb.WriteString(" is related to:")
		for _, t := range triples {
			_, _ = fmt.Fprintf(&sb, "\n  - [%s] %s (%s)", t.Predicate, t.Object.Name, t.Object.Type)
		}
	}
	return sb.String(), nil
}

// SearchRelated finds nodes related to any of the given names.
func SearchRelated(ctx context.Context, g Graph, names []string, maxDepth int) ([]Triple, error) {
	var allTriples []Triple
	seen := make(map[string]bool)

	for _, name := range names {
		nodes, err := g.FindByName(ctx, name)
		if err != nil {
			continue
		}
		for _, node := range nodes {
			triples, err := g.Traverse(ctx, node.ID, maxDepth)
			if err != nil {
				continue
			}
			for _, t := range triples {
				key := t.Subject.ID + "-" + t.Predicate + "-" + t.Object.ID
				if !seen[key] {
					seen[key] = true
					allTriples = append(allTriples, t)
				}
			}
		}
	}
	return allTriples, nil
}
