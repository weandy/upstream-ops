package siteops

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"time"
)

type DefaultProber struct {
	client UpstreamClient
}

var _ Prober = (*DefaultProber)(nil)

func NewProber(client UpstreamClient) *DefaultProber {
	return &DefaultProber{client: client}
}

func probeCapability(status int, err error, at time.Time) EndpointCapability {
	capability := EndpointCapability{State: CapabilitySupported, HTTPStatus: status, CheckedAt: at}
	if err == nil {
		return capability
	}
	capability.Reason = errorCode(err)
	switch {
	case errors.Is(err, ErrForbidden):
		capability.State = CapabilityForbidden
	case errors.Is(err, ErrOpsDisabled):
		capability.State = CapabilityOpsDisabled
		capability.Reason = "OPS_DISABLED"
	case errors.Is(err, ErrUnsupportedRoute):
		capability.State = CapabilityUnsupported
	default:
		capability.State = CapabilityError
	}
	return capability
}

func (p *DefaultProber) ProbeInstance(ctx context.Context, inst TargetEndpoint, existing CapabilitiesMap) (*ProbeReport, error) {
	report := &ProbeReport{
		InstanceID: inst.InstanceID, Reachability: ReachabilityUnknown,
		Capabilities: make(CapabilitiesMap), ProbedAt: time.Now().UTC(),
	}
	maps.Copy(report.Capabilities, existing)
	health, err := p.client.ProbeHealth(ctx, inst)
	if health != nil {
		report.Health = *health
		// Keep the report safe even when another client implementation supplies the health result.
		report.Health.SanitizedError = SanitizeErrorText(health.SanitizedError, 0)
	}
	if report.Health.CheckedAt.IsZero() {
		report.Health.CheckedAt = time.Now().UTC()
	}
	report.Reachability = ReachabilityUnreachable
	if report.Health.Reachable {
		report.Reachability = ReachabilityReachable
	} else if err == nil {
		err = errUpstreamStatus
	}
	report.Capabilities[EndpointHealth] = probeCapability(report.Health.HTTPStatus, err, report.Health.CheckedAt)
	if err := ctx.Err(); err != nil {
		return report, err
	}

	// Reprobe the primary route even when polling currently uses the legacy fallback.
	_, meta, err := p.client.FetchOpsSnapshot(ctx, inst, nil)
	checkedAt := time.Now().UTC()
	if meta.FallbackUsed == EndpointOpsOverview {
		primary := probeCapability(http.StatusNotFound, ErrUnsupportedRoute, checkedAt)
		primary.FallbackTarget = EndpointOpsOverview
		report.Capabilities[EndpointOpsSnapshotV2] = primary
		report.Capabilities[EndpointOpsOverview] = probeCapability(meta.HTTPStatus, err, checkedAt)
	} else {
		report.Capabilities[EndpointOpsSnapshotV2] = probeCapability(meta.HTTPStatus, err, checkedAt)
	}
	if errors.Is(err, ErrUnauthorized) {
		return report, ErrUnauthorized
	}
	steps := []struct {
		endpoint string
		fetch    func() (UpstreamFetchMeta, error)
	}{
		{EndpointOpsConcurrency, func() (UpstreamFetchMeta, error) {
			_, meta, err := p.client.FetchConcurrency(ctx, inst)
			return meta, err
		}},
		{EndpointOpsAccountAvail, func() (UpstreamFetchMeta, error) {
			_, meta, err := p.client.FetchAccountAvailability(ctx, inst)
			return meta, err
		}},
		{EndpointGroupsAll, func() (UpstreamFetchMeta, error) {
			_, meta, err := p.client.FetchGroupsAll(ctx, inst)
			return meta, err
		}},
		{EndpointSystemVersion, func() (UpstreamFetchMeta, error) {
			version, meta, err := p.client.FetchSystemVersion(ctx, inst)
			if err == nil && version != nil {
				report.Version = SanitizeErrorText(version.Version, 0)
			}
			return meta, err
		}},
	}
	if p1a, ok := p.client.(P1AClient); ok {
		steps = append(steps,
			struct {
				endpoint string
				fetch    func() (UpstreamFetchMeta, error)
			}{EndpointOpsAlertEvents, func() (UpstreamFetchMeta, error) {
				_, meta, err := p1a.FetchOpsAlertEvents(ctx, inst)
				return meta, err
			}},
			struct {
				endpoint string
				fetch    func() (UpstreamFetchMeta, error)
			}{EndpointOpsIngressHealth, func() (UpstreamFetchMeta, error) {
				_, meta, err := p1a.FetchIngressRejectHealth(ctx, inst)
				return meta, err
			}},
			struct {
				endpoint string
				fetch    func() (UpstreamFetchMeta, error)
			}{EndpointOpsAuthCacheHealth, func() (UpstreamFetchMeta, error) {
				_, meta, err := p1a.FetchAuthCacheHealth(ctx, inst)
				return meta, err
			}},
			struct {
				endpoint string
				fetch    func() (UpstreamFetchMeta, error)
			}{EndpointAccounts, func() (UpstreamFetchMeta, error) {
				_, meta, err := p1a.FetchAccountsDirectory(ctx, inst)
				return meta, err
			}},
		)
	}
	if p1b, ok := p.client.(P1BClient); ok {
		steps = append(steps,
			struct {
				endpoint string
				fetch    func() (UpstreamFetchMeta, error)
			}{EndpointGroupsCapacitySummary, func() (UpstreamFetchMeta, error) {
				_, meta, err := p1b.FetchGroupCapacitySummary(ctx, inst)
				return meta, err
			}},
		)
	}
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		meta, err := step.fetch()
		report.Capabilities[step.endpoint] = probeCapability(meta.HTTPStatus, err, time.Now().UTC())
		if errors.Is(err, ErrUnauthorized) {
			return report, ErrUnauthorized
		}
	}
	return report, ctx.Err()
}
