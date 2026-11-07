package daemon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

type consumerFacts struct {
	record    record
	runtime   runtimeRecord
	resources resourceGeneration
	sequence  int64
}

func executionAttempt(i v1.Instance) string {
	if i.Execution != nil {
		return i.Execution.Attempt
	}
	return ""
}

// consumerRecords reads only this transaction's authority; supplied summaries are not proof.
func consumerRecords(tx *bolt.Tx, selected record) (f consumerFacts, err error) {
	id := selected.Instance.ID
	if err = decodeRecord(tx.Bucket([]byte("instances")).Get([]byte(id)), id, &f.record); err != nil {
		return f, err
	}
	i := f.record.Instance
	if executionAttempt(i) != executionAttempt(selected.Instance) {
		return f, problem("conflict", "consumer attempt changed before eligibility projection")
	}
	if i.Execution != nil {
		if !validID(i.Execution.Attempt) {
			return f, problem("state_corrupt", "invalid consumer attempt")
		}
		seen := map[string]bool{}
		for _, receipt := range i.Execution.NativeCleanup {
			if receipt.Validate() != nil || receipt.Attempt != i.Execution.Attempt || !hasComponent(i.Execution.Components, receipt.Component) || seen[receipt.Component] {
				return f, problem("state_corrupt", "invalid consumer native observation")
			}
			seen[receipt.Component] = true
		}
	}
	if raw := tx.Bucket([]byte("runtime")).Get([]byte(id)); raw != nil {
		f.runtime, err = decodeRuntime(raw)
		if err != nil {
			return f, err
		}
		if f.runtime.LaunchPurpose != "" && f.runtime.LaunchPurpose != "application" {
			return f, problem("state_corrupt", "invalid consumer launch purpose")
		}
		for _, group := range f.runtime.Groups {
			if group.Component != "" && !manifest.ValidIdentifier(group.Component) {
				return f, problem("state_corrupt", "invalid consumer component identity")
			}
		}
		if g := f.runtime.Gateway; g != nil && (g.ID == "" || g.Endpoint == "" || g.Scope == "" || g.Host == "" || !json.Valid(g.Route)) {
			return f, problem("state_corrupt", "invalid consumer gateway authority")
		}
	}
	if raw := tx.Bucket([]byte("resources")).Get([]byte(id)); raw != nil {
		f.resources, err = decodeResources(raw)
		if err != nil {
			return f, err
		}
	}
	if raw := tx.Bucket([]byte("attempt_sequences")).Get([]byte(id)); raw != nil {
		f.sequence, err = strconv.ParseInt(string(raw), 10, 64)
		if err != nil || f.sequence <= 0 {
			return f, problem("state_corrupt", "invalid consumer acceptance sequence")
		}
	}
	if b := i.Task; b != nil {
		if !validID(b.InstanceID) || !validID(b.Attempt) || !validID(b.Generation) || !manifest.ValidIdentifier(b.Name) || b.InstanceID == id || !validID(executionAttempt(i)) {
			return f, problem("state_corrupt", "invalid dependent consumer binding")
		}
		var parent record
		if err := decodeRecord(tx.Bucket([]byte("instances")).Get([]byte(b.InstanceID)), b.InstanceID, &parent); err != nil {
			return f, err
		}
		if parent.Instance.Task != nil || parent.Instance.Plan.Lifetime != v1.Persistent || i.Plan.Lifetime != v1.Disposable || f.resources.ID != "" {
			return f, problem("state_corrupt", "invalid dependent consumer ownership")
		}
		if active(i.Status) || i.Execution.CleanupFailure != "" {
			generation := executionAttempt(parent.Instance)
			if raw := tx.Bucket([]byte("resources")).Get([]byte(b.InstanceID)); raw != nil {
				resources, err := decodeResources(raw)
				if err != nil {
					return f, err
				}
				if resources.ID != "" {
					generation = resources.ID
				}
			}
			if executionAttempt(parent.Instance) != b.Attempt || generation != b.Generation {
				return f, problem("state_corrupt", "live dependent consumer parent changed")
			}
		}
	}
	return f, nil
}

// consumerErased recognizes completed scene/task erasure without assigning a
// no-launch history. Pending deletion still fences the selected owner.
func consumerErased(tx *bolt.Tx, r record) (bool, error) {
	i := r.Instance
	if i.Status != v1.Destroyed || i.Execution != nil {
		return false, nil
	}
	if i.Operation.Status != v1.Destroyed || r.LeaseHash != "" || i.LeaseExpiresAt != "" || len(r.NativeAbandonments) != 0 || len(i.NativeOwnershipWarnings) != 0 || i.Plan.Lifetime != v1.Persistent && i.Plan.Lifetime != v1.Disposable {
		return false, problem("state_corrupt", "invalid erased consumer authority")
	}
	if b := i.Task; b != nil {
		if !validID(b.InstanceID) || !validID(b.Attempt) || !validID(b.Generation) || !manifest.ValidIdentifier(b.Name) || b.InstanceID == i.ID || i.Plan.Lifetime != v1.Disposable {
			return false, problem("state_corrupt", "invalid erased dependent consumer binding")
		}
		parent, err := load(tx, b.InstanceID)
		if err != nil {
			return false, err
		}
		if parent.Instance.Task != nil || parent.Instance.Plan.Lifetime != v1.Persistent {
			return false, problem("state_corrupt", "invalid erased dependent consumer parent")
		}
	}
	for _, name := range []string{"runtime", "snapshots", "secrets", "attempt_sequences"} {
		if tx.Bucket([]byte(name)).Get([]byte(i.ID)) != nil {
			return false, problem("state_corrupt", "erased dependent consumer retains private authority")
		}
	}
	if raw := tx.Bucket([]byte("resources")).Get([]byte(i.ID)); raw != nil {
		g, err := decodeResources(raw)
		if err != nil {
			return false, err
		}
		if g.ID != "" || g.Endpoint != "" || len(g.Resources) != 0 || len(g.Containers) != 0 || len(g.BuildImages) != 0 || len(g.Initialized) != 0 {
			return false, problem("state_corrupt", "erased dependent consumer retains resource authority")
		}
	}
	if err := tx.Bucket([]byte("instances")).ForEach(func(key, raw []byte) error {
		var child record
		if err := decodeRecord(raw, string(key), &child); err != nil {
			return err
		}
		if child.Instance.Task != nil && child.Instance.Task.InstanceID == i.ID {
			if i.Task != nil {
				return problem("state_corrupt", "erased task cannot retain dependent consumers")
			}
			erased, err := consumerErased(tx, child)
			if err != nil {
				return err
			}
			if !erased {
				return problem("cleanup_failed", "erased scene retains dependent consumers")
			}
		}
		return nil
	}); err != nil {
		return false, err
	}
	allocations, err := decodeSharedAllocations(tx.Bucket([]byte("shared_allocations")).Get([]byte(i.ID)))
	if err != nil {
		return false, err
	}
	for _, allocation := range allocations {
		if !allocation.Released {
			return false, problem("cleanup_failed", "erased consumer retains shared allocation authority")
		}
	}
	prefix := []byte(i.ID + "/")
	if k, _ := tx.Bucket([]byte("attempts")).Cursor().Seek(prefix); bytes.HasPrefix(k, prefix) {
		return false, problem("state_corrupt", "erased dependent consumer retains attempt authority")
	}
	pending := tx.Bucket([]byte("evidence_deletions")).Cursor()
	if k, raw := pending.Seek(prefix); bytes.HasPrefix(k, prefix) {
		var d evidenceDeletion
		if string(k) != i.ID+"/" || json.Unmarshal(raw, &d) != nil || d.InstanceID != i.ID || d.Attempt != "" {
			return false, problem("state_corrupt", "invalid erased dependent consumer deletion")
		}
		if next, _ := pending.Next(); bytes.HasPrefix(next, prefix) {
			return false, problem("state_corrupt", "erased dependent consumer retains attempt deletion")
		}
		return false, problem("cleanup_failed", "consumer evidence erasure remains pending")
	}
	return true, nil
}

func earlierConsumerBoot(recorded, current string) bool {
	valid := func(s string) bool {
		if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return false
		}
		b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
		return err == nil && len(b) == 16
	}
	return valid(recorded) && valid(current) && !strings.EqualFold(recorded, current)
}

func consumerPreviousBoot(j runtimeRecord, attempt, boot string) bool {
	if j.Attempt != attempt || len(j.Groups) == 0 && !j.LaunchPending {
		return false
	}
	if j.LaunchPending && !earlierConsumerBoot(j.PendingBoot, boot) {
		return false
	}
	for _, g := range j.Groups {
		if !earlierConsumerBoot(g.Boot, boot) {
			return false
		}
	}
	return true
}

// consumerOwnEligibility evaluates actual journals; no cached settlement is consulted.
func consumerOwnEligibility(tx *bolt.Tx, f consumerFacts, boot string) v1.ConsumerSettlement {
	i, j, g := f.record.Instance, f.runtime, f.resources
	s := v1.ConsumerSettlement{InstanceID: i.ID, Attempt: executionAttempt(i), State: v1.ConsumerSettled, Cause: v1.SettlementVerifiedRelease, Blockers: []v1.ConsumerBlocker{}}
	block := func(code, component string, active bool) {
		s.Blockers = append(s.Blockers, v1.ConsumerBlocker{Code: code, InstanceID: i.ID, Component: component})
		if active {
			s.State, s.Cause = v1.ConsumerUnsettled, v1.SettlementIncomplete
		} else if s.State == v1.ConsumerSettled {
			s.State, s.Cause = v1.ConsumerUnknown, v1.SettlementUnproven
		}
	}
	if i.Status == v1.Preparing || i.Status == v1.Starting || i.Status == v1.RuntimeReady || i.Status == v1.Stopping || i.Execution != nil && i.Execution.Kept {
		block("active_consumer", "", true)
	}
	for _, abandoned := range f.record.NativeAbandonments {
		old := earlierConsumerBoot(abandoned.Boot, boot)
		if abandoned.Pending {
			old = old && earlierConsumerBoot(abandoned.PendingBoot, boot)
		}
		for _, group := range abandoned.Groups {
			old = old && earlierConsumerBoot(group.Boot, boot)
		}
		if !old {
			block("native_abandoned", "", false)
		}
	}
	if i.Execution == nil {
		key, _ := tx.Bucket([]byte("attempts")).Cursor().Seek([]byte(i.ID + "/"))
		metadata := f.sequence == 0 && !bytes.HasPrefix(key, []byte(i.ID+"/")) && tx.Bucket([]byte("runtime")).Get([]byte(i.ID)) == nil && tx.Bucket([]byte("resources")).Get([]byte(i.ID)) == nil && len(f.record.NativeAbandonments) == 0 && i.Task == nil
		if i.Status == v1.Prepared {
			metadata = metadata && i.Operation.Allocations[0].Phase == "effect" && tx.Bucket([]byte("snapshots")).Get([]byte(i.ID)) != nil
		}
		if i.Status != v1.Prepared && i.Status != v1.Cancelled && i.Status != v1.Interrupted {
			metadata = false
		}
		if !metadata {
			block("metadata_unproven", "", false)
		} else if s.State == v1.ConsumerSettled {
			s.Cause = v1.SettlementNotStarted
		}
		return s
	}
	s.Generation = consumerGeneration(f)
	if j.Attempt == "" {
		block("runtime_missing", "", false)
	} else if j.Attempt != s.Attempt || f.sequence == 0 {
		block("attempt_mismatch", "", false)
	}
	released := consumerReleaseMatches(tx, f)
	if released && s.State == v1.ConsumerSettled {
		s.Cause, s.ObservedAt = j.Release.Cause, j.Release.ObservedAt
	}
	previous := consumerPreviousBoot(j, s.Attempt, boot)
	if j.LaunchPending && !previous {
		block("pending_launch", "", false)
	}
	proof := previous || released
	for _, group := range j.Groups {
		if group.Protocol != 0 && group.Attempt != s.Attempt {
			block("attempt_mismatch", group.Component, false)
			continue
		}
		if previous || released {
			continue
		}
		// Public component summaries cannot identify which exact launch was observed.
		block("native_unproven", group.Component, false)
	}
	for _, container := range g.Containers {
		if container.Attempt != s.Attempt && !container.Removed {
			block("container_attempt_mismatch", container.Component, false)
		}
		if !container.Removed {
			block("container_not_removed", container.Component, false)
		} else if container.Attempt == s.Attempt {
			proof = true
		}
	}
	if !proof {
		block("no_release_evidence", "", false)
	}
	if j.Gateway != nil || j.GatewayOnlyCleanup {
		block("gateway_pending", "", false)
	}
	for _, resource := range g.Resources {
		if resource.Kind == "network" && !resource.Removed {
			block("network_not_removed", "", false)
		}
	}
	if i.Execution.CleanupFailure != "" || len(i.Execution.CleanupDetails) > 0 {
		block("cleanup_incomplete", "", true)
	}
	if s.State == v1.ConsumerSettled && previous && !released {
		s.Cause = v1.SettlementPreviousBoot
	}
	return s
}

// consumerEligibility reads all independent obligations in one supplied transaction.
// Actual mutation callers must separately join their in-memory owner under existing locks.
func consumerEligibility(tx *bolt.Tx, r record, currentBoot string) (v1.ConsumerSettlement, error) {
	f, err := consumerRecords(tx, r)
	if err != nil {
		return v1.ConsumerSettlement{}, err
	}
	s := consumerOwnEligibility(tx, f, currentBoot)
	// Tasks borrow only a scene, never another task: reject nesting instead of traversing a graph.
	var tasks []record
	parents := map[string]bool{}
	err = tx.Bucket([]byte("instances")).ForEach(func(key, raw []byte) error {
		var child record
		if err := decodeRecord(raw, string(key), &child); err != nil {
			return err
		}
		if child.Instance.Task != nil {
			parents[child.Instance.Task.InstanceID] = true
			if child.Instance.Task.InstanceID == s.InstanceID {
				erased, err := consumerErased(tx, child)
				if err != nil || erased {
					return err
				}
				tasks = append(tasks, child)
			}
		}
		return nil
	})
	if err != nil {
		return s, err
	}
	if f.record.Instance.Task != nil && len(tasks) > 0 {
		return s, problem("state_corrupt", "tasks cannot own dependent consumers")
	}
	if f.record.Instance.Execution == nil && len(tasks) > 0 {
		s.Blockers = append(s.Blockers, v1.ConsumerBlocker{Code: "metadata_unproven", InstanceID: s.InstanceID})
		if s.State == v1.ConsumerSettled {
			s.State, s.Cause = v1.ConsumerUnknown, v1.SettlementUnproven
		}
	}
	for _, child := range tasks {
		if parents[child.Instance.ID] {
			return s, problem("state_corrupt", "nested dependent consumer authority")
		}
		facts, err := consumerRecords(tx, child)
		if err != nil {
			return s, err
		}
		result := consumerOwnEligibility(tx, facts, currentBoot)
		if result.State != v1.ConsumerSettled {
			s.Blockers = append(s.Blockers, result.Blockers...)
			s.Blockers = append(s.Blockers, v1.ConsumerBlocker{Code: "dependent_unsettled", InstanceID: child.Instance.ID})
			if result.State == v1.ConsumerUnsettled || s.State == v1.ConsumerSettled {
				s.State, s.Cause = result.State, result.Cause
			}
		}
	}
	slices.SortFunc(s.Blockers, func(a, b v1.ConsumerBlocker) int {
		return strings.Compare(a.Code+"/"+a.InstanceID+"/"+a.Component, b.Code+"/"+b.InstanceID+"/"+b.Component)
	})
	s.Blockers = slices.Compact(s.Blockers)
	if len(s.Blockers) > 100 {
		s.OmittedBlockers = len(s.Blockers) - 100
		s.Blockers = s.Blockers[:100]
	}
	if s.State == v1.ConsumerUnsettled {
		for _, b := range s.Blockers {
			if b.Code == "active_consumer" {
				s.Cause = v1.SettlementActive
			}
		}
	}
	if s.State != v1.ConsumerSettled {
		s.ObservedAt = ""
	}
	return s, s.Validate()
}

// consumerEffectRevision binds a producer's release fact to exact current effects.
// It excludes outcomes, collection strings and all release/settlement summaries.
func consumerEffectRevision(tx *bolt.Tx, r record) (string, error) {
	f, err := consumerRecords(tx, r)
	if err != nil {
		return "", err
	}
	type dependent struct {
		ID, Attempt string
		Binding     *v1.TaskBinding
	}
	type abandoned struct {
		Attempt, Boot, PendingBoot string
		Pending                    bool
		Groups                     []native.Identity
	}
	effects := struct {
		ID, Attempt, Generation, Endpoint, RuntimeAttempt, PendingBoot, LaunchPurpose string
		Sequence                                                                      int64
		Task                                                                          *v1.TaskBinding
		Pending                                                                       bool
		Groups                                                                        []native.Identity
		Gateway                                                                       *gateway.Intent
		Containers                                                                    []ownedResource
		Networks                                                                      map[string]ownedResource
		Abandoned                                                                     []abandoned
		Dependents                                                                    []dependent
	}{ID: f.record.Instance.ID, Attempt: executionAttempt(f.record.Instance), Generation: f.resources.ID, Endpoint: f.resources.Endpoint, RuntimeAttempt: f.runtime.Attempt, Sequence: f.sequence, Task: f.record.Instance.Task, Pending: f.runtime.LaunchPending, PendingBoot: f.runtime.PendingBoot, LaunchPurpose: f.runtime.LaunchPurpose, Groups: f.runtime.Groups, Gateway: f.runtime.Gateway, Containers: f.resources.Containers, Networks: map[string]ownedResource{}}
	for name, resource := range f.resources.Resources {
		if resource.Kind == "network" {
			effects.Networks[name] = resource
		}
	}
	for _, a := range f.record.NativeAbandonments {
		effects.Abandoned = append(effects.Abandoned, abandoned{a.Attempt, a.Boot, a.PendingBoot, a.Pending, a.Groups})
	}
	err = tx.Bucket([]byte("instances")).ForEach(func(key, raw []byte) error {
		var child record
		if err := decodeRecord(raw, string(key), &child); err != nil {
			return err
		}
		if child.Instance.Task != nil && child.Instance.Task.InstanceID == effects.ID {
			erased, err := consumerErased(tx, child)
			if err != nil || erased {
				return err
			}
			if _, err := consumerRecords(tx, child); err != nil {
				return err
			}
			effects.Dependents = append(effects.Dependents, dependent{child.Instance.ID, executionAttempt(child.Instance), child.Instance.Task})
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(effects)
	if err != nil {
		return "", err
	}
	return hash(string(data)), nil
}
