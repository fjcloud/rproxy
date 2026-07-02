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
	mu           sync.RWMutex
	routes       map[string]Route // fqdn -> Route
	podmanClient *podman.Client
	certManager  *certs.Manager
	config       *config.Config
	certWorkCh   chan []string // FQDNs needing cert work, buffered to avoid blocking route updates
}

// NewRouter creates a new Router.
func NewRouter(cfg *config.Config, pClient *podman.Client, cMgr *certs.Manager) *Router {
	return &Router{
		routes:       make(map[string]Route),
		podmanClient: pClient,
		certManager:  cMgr,
		config:       cfg,
		certWorkCh:   make(chan []string, 1),
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

// RunCertManager processes certificate renewals independently of route updates.
// It reads batches of FQDNs from the cert work channel and renews them sequentially,
// waiting dnsChallengeTTLWait between each to let DNS caches expire (all domains share
// the same _acme-challenge CNAME target so their TXT records would otherwise collide).
func (r *Router) RunCertManager(ctx context.Context) {
	const dnsChallengeTTLWait = 310 * time.Second
	slog.Info("Starting cert manager")
	for {
		select {
		case fqdns := <-r.certWorkCh:
			slog.Info("CertManager: Processing certificate renewals", "count", len(fqdns), "fqdns", fqdns)
			for i, fqdn := range fqdns {
				slog.Info("CertManager: Checking certificate", "fqdn", fqdn)
				r.certManager.CheckAndManageCert(fqdn)
				if i < len(fqdns)-1 {
					slog.Info("CertManager: Waiting for DNS TTL to expire before next renewal", "wait", dnsChallengeTTLWait)
					select {
					case <-time.After(dnsChallengeTTLWait):
					case <-ctx.Done():
						slog.Info("CertManager: Stopping during TTL wait.")
						return
					}
				}
			}
			slog.Info("CertManager: Batch complete")
		case <-ctx.Done():
			slog.Info("Stopping cert manager.")
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
	var fqdnsNeedingCerts []string // Collect FQDNs that need certificate management

	// 1. List containers
	containers, err := r.podmanClient.ListContainers()
	if err != nil {
		slog.Error("Router: Error listing containers", "error", err)
		return // Keep old map on error
	}

	// 2. Inspect each container found to get IP
	var wg sync.WaitGroup
	var inspectMutex sync.Mutex // Mutex to protect access to newRoutes map and fqdnsNeedingCerts slice from goroutines

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
				// Collect FQDN for certificate management (will be processed sequentially later)
				fqdnsNeedingCerts = append(fqdnsNeedingCerts, c.FQDN)
			} else {
				// Route exists and is unchanged, just copy it
				newRoutes[c.FQDN] = newRoute
			}
			inspectMutex.Unlock()

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

	// 3. Hand off certificate management to the dedicated cert manager goroutine.
	// This avoids blocking the route update loop during long cert renewals.
	if len(fqdnsNeedingCerts) > 0 {
		select {
		case r.certWorkCh <- fqdnsNeedingCerts:
			slog.Info("Router: Queued certificate management", "count", len(fqdnsNeedingCerts), "fqdns", fqdnsNeedingCerts)
		default:
			slog.Warn("Router: Cert manager busy, cert renewal will retry on next route change", "fqdns", fqdnsNeedingCerts)
		}
	}
} 