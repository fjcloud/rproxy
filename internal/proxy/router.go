package proxy

import (
	"context"
	"log/slog"
	"rproxy/internal/certs"    // Assuming module path is rproxy
	"rproxy/internal/config"
	"rproxy/internal/podman"
	"strconv"
	"sync"
	"time"
)

// Route stores target backend info.
type Route struct {
	TargetIP   string
	TargetPort int
}

// Router manages the dynamic routing table.
type Router struct {
	mu         sync.RWMutex
	routes     map[string]Route // fqdn -> Route
	podmanClient *podman.Client
	certManager *certs.Manager
	config     *config.Config
}

// NewRouter creates a new Router.
func NewRouter(cfg *config.Config, pClient *podman.Client, cMgr *certs.Manager) *Router {
	return &Router{
		routes:     make(map[string]Route),
		podmanClient: pClient,
		certManager: cMgr,
		config:     cfg,
	}
}

// GetRoute finds the route for a given FQDN.
func (r *Router) GetRoute(fqdn string) (Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	route, exists := r.routes[fqdn]
	return route, exists
}

// RunUpdateLoop starts the periodic route update process.
func (r *Router) RunUpdateLoop(ctx context.Context) {
	slog.Info("Starting route update loop", "interval", r.config.UpdateInterval)
	ticker := time.NewTicker(r.config.UpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.updateRoutes(ctx)
		case <-ctx.Done():
			slog.Info("Stopping route update loop.")
			return
		}
	}
}

// updateRoutes discovers containers and updates the routing map.
func (r *Router) updateRoutes(ctx context.Context) {
	// Get copy of current map to check for changes
	r.mu.RLock()
	oldRoutes := make(map[string]Route, len(r.routes))
	for k, v := range r.routes {
		oldRoutes[k] = v
	}
	r.mu.RUnlock()

	newRoutes := make(map[string]Route)
	routesChanged := false

	// 1. List containers
	containers, err := r.podmanClient.ListContainers()
	if err != nil {
		slog.Error("Router: Error listing containers", "error", err)
		return // Keep old map on error
	}

	// 2. Inspect each container found to get IP
	var wg sync.WaitGroup
	var inspectMutex sync.Mutex // Mutex to protect access to newRoutes map from goroutines

	for _, container := range containers {
		wg.Add(1)
		go func(c podman.ContainerInfo) {
			defer wg.Done()

			inspectData, err := r.podmanClient.InspectContainer(c.ID)
			if err != nil {
				slog.Error("Router: Error inspecting container", "name", c.Name, "id", c.ID, "error", err)
				return
			}

			var ipAddress string
			if inspectData.NetworkSettings.Networks != nil {
				for _, netDetails := range inspectData.NetworkSettings.Networks {
					if netDetails.IPAddress != "" {
						ipAddress = netDetails.IPAddress
						break
					}
				}
			}
			if ipAddress == "" {
				slog.Warn("Router: Could not find IP address for container", "name", c.Name, "id", c.ID)
				return
			}

			exposedPort, err := strconv.Atoi(c.ExposedPort)
			if err != nil {
				slog.Error("Router: Invalid exposed-port label", "label", c.ExposedPort, "name", c.Name, "id", c.ID, "error", err)
				return
			}

			newRoute := Route{
				TargetIP:   ipAddress,
				TargetPort: exposedPort,
			}

			// Check if route is new or changed before logging/managing cert
			inspectMutex.Lock()
			oldRoute, exists := oldRoutes[c.FQDN]
			if !exists || oldRoute != newRoute {
				routesChanged = true
				slog.Info("Router: Updating route", "fqdn", c.FQDN, "targetIP", ipAddress, "targetPort", exposedPort, "container", c.Name)
				newRoutes[c.FQDN] = newRoute
				inspectMutex.Unlock() // Unlock before cert check
				// Trigger certificate check immediately for new/changed FQDN
				r.certManager.CheckAndManageCert(c.FQDN)
			} else {
				// Route exists and is unchanged, just copy it
				newRoutes[c.FQDN] = newRoute
				inspectMutex.Unlock()
			}

		}(container)
	}
	wg.Wait()

	// Update the global routing map only if changes were detected
	if routesChanged {
		r.mu.Lock()
		r.routes = newRoutes
		slog.Info("Router: Route map updated", "active_routes", len(r.routes))
		r.mu.Unlock()
	}
} 