package httpapi

import (
	"net/http"

	"v2link-go/internal/regions"
)

// regionsData 是 /api/regions/probes 的 data 形态。
type regionsData struct {
	UpdatedAt int64                   `json:"updatedAt"`
	Probes    []regions.SnapshotProbe `json:"probes"`
	History   []regions.Round         `json:"history"`
}

// handleRegions 地区连通性快照 + 最近几轮历史（内存态，不落库）。
func (s *Server) handleRegions(ctx *Context) (Response, error) {
	updatedAt, probes := s.probe.Snapshot()
	return dataJSON(http.StatusOK, regionsData{
		UpdatedAt: updatedAt,
		Probes:    probes,
		History:   s.probe.History(),
	}), nil
}
