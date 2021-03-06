// Copyright 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package discovery

import (
	log "github.com/sirupsen/logrus"

	"github.com/wavefronthq/wavefront-collector-for-kubernetes/internal/discovery"
	"github.com/wavefronthq/wavefront-collector-for-kubernetes/internal/leadership"
	"github.com/wavefronthq/wavefront-collector-for-kubernetes/internal/metrics"

	gm "github.com/rcrowley/go-metrics"

	"k8s.io/client-go/kubernetes"
)

const (
	subscriberName = "discovery.manager"
)

var (
	discoveryEnabled gm.Counter
)

func init() {
	discoveryEnabled = gm.GetOrRegisterCounter("discovery.enabled", gm.DefaultRegistry)
}

// RunConfig encapsulates the runtime configuration required for a discovery manager
type RunConfig struct {
	KubeClient      kubernetes.Interface
	DiscoveryConfig discovery.Config
	Handler         metrics.ProviderHandler
	Lister          discovery.ResourceLister
	Daemon          bool
}

// Manager manages the discovery of kubernetes targets based on annotations or configuration rules.
type Manager struct {
	runConfig       RunConfig
	discoverer      discovery.Discoverer
	podListener     *podHandler
	serviceListener *serviceHandler
	leadershipMgr   *leadership.Manager
	stopCh          chan struct{}
}

// NewDiscoveryManager creates a new instance of a discovery manager based on the given configuration.
func NewDiscoveryManager(cfg RunConfig) *Manager {
	mgr := &Manager{
		runConfig:  cfg,
		stopCh:     make(chan struct{}),
		discoverer: newDiscoverer(cfg.Handler, cfg.DiscoveryConfig, cfg.Lister),
	}
	mgr.leadershipMgr = leadership.NewManager(mgr, subscriberName, cfg.KubeClient)
	return mgr
}

func (dm *Manager) Start() {
	log.Infof("Starting discovery manager")
	discoveryEnabled.Inc(1)

	dm.stopCh = make(chan struct{})

	// init discovery handlers
	dm.podListener = newPodHandler(dm.runConfig.KubeClient, dm.discoverer)
	dm.serviceListener = newServiceHandler(dm.runConfig.KubeClient, dm.discoverer)
	dm.podListener.start()

	if dm.runConfig.Daemon {
		// in daemon mode, service discovery is performed by only one collector agent in a cluster
		// kick off leader election to determine if this agent should handle it
		dm.leadershipMgr.Start()
	} else {
		dm.serviceListener.start()
	}
}

func (dm *Manager) Stop() {
	log.Infof("Stopping discovery manager")
	discoveryEnabled.Dec(1)

	leadership.Unsubscribe(subscriberName)
	dm.podListener.stop()
	dm.serviceListener.stop()
	close(dm.stopCh)

	dm.discoverer.Stop()
	dm.discoverer.DeleteAll()
}

func (dm *Manager) Resume() {
	log.Infof("elected leader: %s starting service discovery", leadership.Leader())
	dm.serviceListener.start()
}

func (dm *Manager) Pause() {
	log.Infof("stopping service discovery. new leader: %s", leadership.Leader())
	dm.serviceListener.stop()
}
