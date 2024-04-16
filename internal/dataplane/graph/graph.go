package graph

import (
	"errors"
	"fmt"
	"os"

	"github.com/dominikbraun/graph"
	"github.com/dominikbraun/graph/draw"
	"github.com/go-logr/logr"
	"github.com/kong/go-database-reconciler/pkg/file"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kong/kubernetes-ingress-controller/v3/internal/dataplane/sendconfig"
)

type Entity struct {
	Name                             string
	Type                             string
	WasRecoveredFromLatestGoodConfig bool
	Raw                              any
}

type EntityHash string

type KongConfigGraph = graph.Graph[EntityHash, Entity]

func hashEntity(entity Entity) EntityHash {
	return EntityHash(entity.Type + ":" + entity.Name)
}

func RenderGraphDOT(g KongConfigGraph, outFilePath string) (string, error) {
	var outFile *os.File
	if outFilePath == "" {
		var err error
		outFile, err = os.CreateTemp("", "*.dot")
		if err != nil {
			return "", fmt.Errorf("failed to create temp file: %w", err)
		}
		defer outFile.Close()
		outFilePath = outFile.Name()
	}
	err := draw.DOT(g, outFile)
	if err != nil {
		return "", fmt.Errorf("failed to render dot file: %w", err)
	}
	return outFilePath, nil
}

// FindConnectedComponents iterates over the graph vertices and runs a DFS on each vertex that has not been visited yet.
func FindConnectedComponents(g KongConfigGraph) ([]KongConfigGraph, error) {
	pm, err := g.PredecessorMap()
	if err != nil {
		return nil, err
	}

	var components []KongConfigGraph
	visited := sets.New[EntityHash]()
	for vertex := range pm {
		if visited.Has(vertex) {
			continue // it was already visited
		}
		component := graph.NewLike[EntityHash, Entity](g)
		if err := graph.DFS[EntityHash, Entity](g, vertex, func(visitedHash EntityHash) bool {
			visitedVertex, props, err := g.VertexWithProperties(visitedHash)
			if err != nil {
				return false // continue DFS, should never happen
			}
			if err := component.AddVertex(visitedVertex, graph.VertexAttributes(props.Attributes)); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
				return false // continue DFS, should never happen
			}
			visited.Insert(visitedHash)
			return false // continue DFS
		}); err != nil {
			return nil, err
		}

		edges, err := g.Edges()
		if err != nil {
			return nil, err
		}
		// TODO: Might we skip edges that were already added?
		for _, edge := range edges {
			_, sourceErr := component.Vertex(edge.Source)
			_, targetErr := component.Vertex(edge.Target)
			if sourceErr == nil && targetErr == nil {
				if err := component.AddEdge(edge.Source, edge.Target); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
					return nil, err
				}
			}
		}

		components = append(components, component)
	}

	return components, nil
}

const (
	ColorAttribute     = "color"
	FillColorAttribute = "fillcolor"

	CACertColor          = "brown"
	ServiceColor         = "coral"
	RouteColor           = "darkkhaki"
	CertificateColor     = "deepskyblue"
	UpstreamColor        = "darkolivegreen"
	TargetColor          = "goldenrod"
	ConsumerColor        = "hotpink"
	PluginColor          = "indianred"
	EntityRecoveredColor = "lime"

	StyleAttribute = "style"
	FilledStyle    = "filled"
)

func coloredVertex(color string) func(*graph.VertexProperties) {
	return graph.VertexAttributes(map[string]string{
		FillColorAttribute: color,
		StyleAttribute:     FilledStyle,
	})
}

func BuildKongConfigGraph(config *file.Content) (KongConfigGraph, error) {
	g := graph.New(hashEntity, graph.Directed(), graph.Acyclic())

	for _, caCert := range config.CACertificates {
		ecac := Entity{Name: *caCert.ID, Type: "ca-certificate", Raw: caCert.DeepCopy()}
		if err := g.AddVertex(ecac, coloredVertex(CACertColor)); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
			return nil, err
		}
	}

	for _, service := range config.Services {
		es := Entity{Name: *service.Name, Type: "service", Raw: service.DeepCopy()}
		if err := g.AddVertex(es, coloredVertex(ServiceColor)); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
			return nil, err
		}

		for _, route := range service.Routes {
			er := Entity{Name: *route.Name, Type: "route", Raw: route.DeepCopy()}
			if err := g.AddVertex(er, coloredVertex(RouteColor)); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
				return nil, err
			}
			if err := g.AddEdge(hashEntity(es), hashEntity(er)); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
				return nil, err
			}

			for _, plugin := range route.Plugins {
				ep := pluginToEntity(plugin)
				if err := g.AddVertex(ep, coloredVertex(PluginColor)); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
					return nil, err
				}
				if err := g.AddEdge(hashEntity(er), hashEntity(ep)); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
					return nil, err
				}
			}
		}

		if service.ClientCertificate != nil {
			ecc := Entity{Name: *service.ClientCertificate.ID, Type: "certificate", Raw: service.ClientCertificate.DeepCopy()}
			if err := g.AddVertex(ecc, coloredVertex(CertificateColor)); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
				return nil, err
			}
			if err := g.AddEdge(hashEntity(es), hashEntity(ecc)); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
				return nil, err
			}
		}

		for _, caCert := range service.CACertificates {
			if err := g.AddEdge(hashEntity(es), hashEntity(Entity{Name: *caCert, Type: "ca-certificate"})); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
				return nil, err
			}
		}
	}

	for _, upstream := range config.Upstreams {
		// TODO: should we resolve edges between upstreams and services?
		eu := Entity{Name: *upstream.Name, Type: "upstream", Raw: upstream.DeepCopy()}
		if err := g.AddVertex(eu, coloredVertex(UpstreamColor)); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
			return nil, err
		}

		for _, target := range upstream.Targets {
			et := Entity{Name: *target.Target.Target, Type: "target"}
			if err := g.AddVertex(et, coloredVertex(TargetColor)); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
				return nil, err
			}
			if err := g.AddEdge(hashEntity(eu), hashEntity(et)); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
				return nil, err
			}
		}
	}

	for _, certificate := range config.Certificates {
		ec := Entity{Name: *certificate.ID, Type: "certificate"}
		if err := g.AddVertex(ec, coloredVertex(CertificateColor)); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
			return nil, err
		}
		for _, sni := range certificate.SNIs {
			esni := Entity{Name: *sni.Name, Type: "sni"}
			if err := g.AddVertex(esni, coloredVertex(CertificateColor)); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
				return nil, err
			}
			if err := g.AddEdge(hashEntity(ec), hashEntity(esni)); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
				return nil, err
			}
		}
	}

	for _, consumer := range config.Consumers {
		ec := Entity{Name: *consumer.Username, Type: "consumer", Raw: consumer.DeepCopy()}
		if err := g.AddVertex(ec, coloredVertex(ConsumerColor)); err != nil {
			return nil, err
		}
		// TODO: handle consumer credentials
	}

	for _, plugin := range config.Plugins {
		// TODO: should we resolve edges for plugins that are not enabled?

		// TODO: should we resolve edges for plugins that refer other entities (e.g. mtls-auth -> ca_certificate)?

		ep := pluginToEntity(&plugin)
		if err := g.AddVertex(ep, coloredVertex(PluginColor)); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
			return nil, err
		}

		if plugin.Service != nil {
			es := Entity{Name: *plugin.Service.ID, Type: "service"}
			if err := g.AddEdge(hashEntity(ep), hashEntity(es)); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
				return nil, err
			}
		}
		if plugin.Route != nil {
			er := Entity{Name: *plugin.Route.ID, Type: "route"}
			if err := g.AddEdge(hashEntity(ep), hashEntity(er)); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
				return nil, err
			}
		}
		if plugin.Consumer != nil {
			ec := Entity{Name: *plugin.Consumer.Username, Type: "consumer"}
			if err := g.AddEdge(hashEntity(ep), hashEntity(ec)); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
				return nil, err
			}
		}
	}

	return g, nil
}

func pluginToEntity(plugin *file.FPlugin) Entity {
	// TODO: how to identify Plugins uniquely when no ID nor instance name is present? If we use Plugin.Name,
	// we will have just one vertex per plugin type, which could result in unwanted connections
	// (e.g. broken Service1 <-> Plugin <-> Service2 where Service1 and Service2 should not be connected).
	if plugin.InstanceName == nil {
		plugin.InstanceName = plugin.Name
	}
	return Entity{Name: *plugin.Name + "/" + *plugin.InstanceName, Type: "plugin", Raw: plugin.DeepCopy()}
}

func BuildKongConfigFromGraph(g KongConfigGraph) (*file.Content, error) {
	adjacencyMap, err := g.AdjacencyMap()
	if err != nil {
		return nil, fmt.Errorf("could not get adjacency map of graph: %w", err)
	}

	kongConfig := &file.Content{}
	for vertex := range adjacencyMap {
		v, err := g.Vertex(vertex)
		if err != nil {
			return nil, fmt.Errorf("could not get vertex %v: %w", vertex, err)
		}
		switch v.Type {
		case "service":
			service := v.Raw.(*file.FService)
			if service.Routes != nil {
				service.Routes = nil
			}
			if v.WasRecoveredFromLatestGoodConfig {
				service.Tags = append(service.Tags, lo.ToPtr("recovered-from-last-valid-config"))
			}
			kongConfig.Services = append(kongConfig.Services, *service)
		case "route":
			route := v.Raw.(*file.FRoute)
			if v.WasRecoveredFromLatestGoodConfig {
				route.Tags = append(route.Tags, lo.ToPtr("recovered-from-last-valid-config"))
			}
			kongConfig.Routes = append(kongConfig.Routes, *route)
		case "certificate":
			certificate := v.Raw.(*file.FCertificate)
			kongConfig.Certificates = append(kongConfig.Certificates, *certificate)
		case "ca-certificate":
			caCertificate := v.Raw.(*file.FCACertificate)
			kongConfig.CACertificates = append(kongConfig.CACertificates, *caCertificate)
		case "consumer":
			consumer := v.Raw.(*file.FConsumer)
			kongConfig.Consumers = append(kongConfig.Consumers, *consumer)
		case "plugin":
			plugin := v.Raw.(*file.FPlugin)
			if v.WasRecoveredFromLatestGoodConfig {
				plugin.Tags = append(plugin.Tags, lo.ToPtr("recovered-from-last-valid-config"))
			}
			kongConfig.Plugins = append(kongConfig.Plugins, *plugin)
		case "upstream":
			upstream := v.Raw.(*file.FUpstream)
			kongConfig.Upstreams = append(kongConfig.Upstreams, *upstream)
		}
	}

	return kongConfig, nil
}

func BuildFallbackKongConfig(
	latestGoodConfig KongConfigGraph,
	currentConfig KongConfigGraph,
	entityErrors []sendconfig.FlatEntityError,
	logger logr.Logger,
) (KongConfigGraph, error) {
	if len(entityErrors) == 0 {
		return nil, errors.New("entityErrors is empty")
	}

	affectedEntities := lo.Map(entityErrors, func(ee sendconfig.FlatEntityError, _ int) EntityHash {
		// TODO: how to make sure identification always works despite entity type?
		// It would be good to have deterministic IDs assigned to all entities so that we can use them here.
		return hashEntity(Entity{Name: ee.Name, Type: ee.Type})
	})

	fallbackConfig, err := currentConfig.Clone()
	if err != nil {
		return nil, fmt.Errorf("could not clone current config")
	}
	for _, entity := range affectedEntities {
		if err := removeSubraphFromVertex(fallbackConfig, entity); err != nil {
			return nil, fmt.Errorf("could not remove subgraph containing entity %s: %w", entity, err)
		}
	}
	// We need to add all connected components that contain affected entities from the latest good config.
	// This should be an opt-in operation, we may as well skip it if the user does not want to recover the entities.
	for _, entity := range affectedEntities {
		if err := addSubgraphFromVertex(latestGoodConfig, fallbackConfig, entity); err != nil {
			return nil, fmt.Errorf("could not add subgraph containing entity %s: %w", entity, err)
		}
	}

	return fallbackConfig, nil
}

func removeSubraphFromVertex(g KongConfigGraph, v EntityHash) error {
	adjacencyMap, err := g.AdjacencyMap()
	if err != nil {
		return fmt.Errorf("could not get adjacency map of graph")
	}

	if _, err := g.Vertex(v); err != nil {
		return fmt.Errorf("could not get vertex %v", v)
	}

	// Run DFS to find all dependent entities and remove them from the graph.
	if err := graph.DFS(g, v, func(visited EntityHash) bool {
		// Remove edges first.
		for neighbour := range adjacencyMap[visited] {
			if err := g.RemoveEdge(visited, neighbour); err != nil {
				return false
			}
		}
		predecesorsMap, err := g.PredecessorMap()
		if err != nil {
			return false
		}
		for neighbour := range predecesorsMap[visited] {
			if err := g.RemoveEdge(neighbour, visited); err != nil {
				return false
			}
		}

		if err := g.RemoveVertex(visited); err != nil {
			return false
		}
		return false // Run DFS until all dependent entities are removed.
	}); err != nil {
		return fmt.Errorf("could not remove connected component containing entity %s: %w", v, err)
	}

	return nil
}

func addSubgraphFromVertex(src KongConfigGraph, dst KongConfigGraph, v EntityHash) error {
	adjacencyMap, err := src.AdjacencyMap()
	if err != nil {
		return fmt.Errorf("could not get adjacency map of graph")
	}

	if _, err := src.Vertex(v); err != nil {
		return fmt.Errorf("could not get vertex %v", v)
	}

	// Run DFS to find all dependent entities and add them to the destination graph.
	if err := graph.DFS(src, v, func(visited EntityHash) bool {
		vertex, props, err := src.VertexWithProperties(visited)
		if err != nil {
			return false
		}
		vertex.WasRecoveredFromLatestGoodConfig = true
		if err := dst.AddVertex(vertex, graph.VertexAttributes(props.Attributes)); err != nil {
			return false
		}

		// Add edges.
		for neighbour := range adjacencyMap[visited] {
			if err := dst.AddEdge(visited, neighbour); err != nil {
				return false
			}
		}

		return false // Run DFS until all dependent entities are added.
	}); err != nil {
		return fmt.Errorf("could not add connected component containing entity %s: %w", v, err)
	}

	predecesorsMap, err := src.PredecessorMap()
	if err != nil {
		return fmt.Errorf("could not get predecessor map of graph")
	}
	predecesors := predecesorsMap[v]
	for predecesor := range predecesors {
		if err := dst.AddEdge(predecesor, v); err != nil {
			return fmt.Errorf("could not add edge from %s to %s: %w", predecesor, v, err)
		}

	}

	return nil
}

func addConnectedComponentToGraph(g KongConfigGraph, component KongConfigGraph) error {
	adjacencyMap, err := component.AdjacencyMap()
	if err != nil {
		return fmt.Errorf("could not get adjacency map of connected component: %w", err)
	}

	for hash := range adjacencyMap {
		vertex, props, err := component.VertexWithProperties(hash)
		if err != nil {
			return fmt.Errorf("failed to get vertex %v: %w", hash, err)
		}
		_ = g.AddVertex(vertex, graph.VertexAttributes(props.Attributes), graph.VertexAttribute(ColorAttribute, EntityRecoveredColor))
	}

	edges, err := component.Edges()
	if err != nil {
		return fmt.Errorf("failed to get edges: %w", err)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge.Source, edge.Target)
	}

	return nil
}

func removeConnectedComponentFromGraph(g KongConfigGraph, component KongConfigGraph) error {
	adjacencyMap, err := component.AdjacencyMap()
	if err != nil {
		return fmt.Errorf("could not get adjacency map of connected component")
	}
	for vertex, neighbours := range adjacencyMap {
		// First remove all edges from the vertex to its neighbours.
		for neighbour := range neighbours {
			_ = g.RemoveEdge(vertex, neighbour)
		}
		_ = g.RemoveVertex(vertex)
	}
	return nil
}

func findConnectedComponentContainingEntity(components []KongConfigGraph, entityHash EntityHash) (KongConfigGraph, error) {
	for _, component := range components {
		_, err := component.Vertex(entityHash)
		if err == nil {
			return component, nil
		}
	}

	return nil, fmt.Errorf("could not find connected component containing entity %s", entityHash)
}
