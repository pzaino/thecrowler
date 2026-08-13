// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package database

import (
	"sort"
	"strings"
	"time"
)

// FleetDBReservedConnections is the number of connections kept outside the
// fleet budget for administration and health checks.
const FleetDBReservedConnections = 3

const (
	FleetMemberCrowlerAPI    = "crowler-api"
	FleetMemberCrowlerEngine = "crowler-engine"
	FleetMemberCrowlerEvents = "crowler-events"
)

// FleetMember identifies one database-consuming process in the fleet.
type FleetMember struct {
	OriginType string `json:"origin_type"`
	OriginName string `json:"origin_name"`
}

// FleetMemberAllocation is the connection quota assigned to a member.
type FleetMemberAllocation struct {
	OriginType     string `json:"origin_type"`
	OriginName     string `json:"origin_name"`
	MaxConnections int    `json:"max_connections"`
}

// FleetDBAllocation is the result of allocating an effective PostgreSQL limit.
type FleetDBAllocation struct {
	EffectiveMaxOpen    int                     `json:"effective_max_open"`
	ReservedConnections int                     `json:"reserved_connections"`
	UsableConnections   int                     `json:"usable_connections"`
	MemberCount         int                     `json:"member_count"`
	Members             []FleetMemberAllocation `json:"members"`
	Valid               bool                    `json:"valid"`
	Reason              string                  `json:"reason,omitempty"`
}

// FleetDBHeartbeatReport is the authoritative, serializable database-budget
// report carried by fleet heartbeat events.
type FleetDBHeartbeatReport struct {
	SchemaVersion       string                  `json:"schema_version"`
	ParentEventID       string                  `json:"parent_event_id"`
	GeneratedAt         time.Time               `json:"generated_at"`
	EffectiveMaxOpen    int                     `json:"effective_max_open"`
	ReservedConnections int                     `json:"reserved_connections"`
	UsableConnections   int                     `json:"usable_connections"`
	MemberCount         int                     `json:"member_count"`
	AllocationCount     int                     `json:"allocation_count"`
	Members             []FleetMemberAllocation `json:"members"`
	Valid               bool                    `json:"valid"`
	Reason              string                  `json:"reason,omitempty"`
}

// NormalizeFleetMembers validates, normalizes, deduplicates and sorts members.
// The complete type/name tuple is the identity; names are not globally unique.
func NormalizeFleetMembers(members []FleetMember) []FleetMember {
	unique := make(map[FleetMember]struct{}, len(members))
	for _, member := range members {
		member.OriginType = strings.ToLower(strings.TrimSpace(member.OriginType))
		member.OriginName = strings.TrimSpace(member.OriginName)
		if !recognizedFleetMemberType(member.OriginType) {
			continue
		}
		unique[member] = struct{}{}
	}

	normalized := make([]FleetMember, 0, len(unique))
	for member := range unique {
		normalized = append(normalized, member)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].OriginType != normalized[j].OriginType {
			return normalized[i].OriginType < normalized[j].OriginType
		}
		return normalized[i].OriginName < normalized[j].OriginName
	})
	return normalized
}

func recognizedFleetMemberType(originType string) bool {
	switch originType {
	case FleetMemberCrowlerAPI, FleetMemberCrowlerEngine, FleetMemberCrowlerEvents:
		return true
	default:
		return false
	}
}

// AllocateFleetDBConnections deterministically divides the usable connections
// among the unique, recognized fleet members.
func AllocateFleetDBConnections(effectiveMaxOpen int, members []FleetMember) FleetDBAllocation {
	normalized := NormalizeFleetMembers(members)
	usable := effectiveMaxOpen - FleetDBReservedConnections
	result := FleetDBAllocation{
		EffectiveMaxOpen: effectiveMaxOpen, ReservedConnections: FleetDBReservedConnections,
		UsableConnections: usable, MemberCount: len(normalized),
		Members: make([]FleetMemberAllocation, 0, len(normalized)),
	}
	if len(normalized) == 0 {
		result.Valid = true
		return result
	}
	if usable < len(normalized) {
		result.Reason = "usable database connections are fewer than recognized fleet members"
		return result
	}

	quota, remainder := usable/len(normalized), usable%len(normalized)
	for i, member := range normalized {
		connections := quota
		if i < remainder {
			connections++
		}
		result.Members = append(result.Members, FleetMemberAllocation{
			OriginType: member.OriginType, OriginName: member.OriginName, MaxConnections: connections,
		})
	}
	result.Valid = true
	return result
}
