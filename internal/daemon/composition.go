package daemon

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/compose"
	"github.com/octacian/backlot/internal/plan"
)

func plannedValue(v v1.Value) v1.PlannedValue {
	if v.Literal != nil || v.Ref != nil {
		return v1.PlannedValue{Literal: v.Literal, Symbolic: v.Ref}
	}
	return v1.PlannedValue{Composition: &v}
}

func resolveComposition(v v1.Value, runtime v1.Runtime, leaf func(v1.Value) (string, error)) (string, error) {
	resolved, err := compose.Resolve(v, runtime, func(v v1.Value) (v1.Value, error) {
		text, err := leaf(v)
		return v1.Value{Literal: &text, Secret: v.Secret}, err
	})
	if err != nil {
		if !errors.Is(err, compose.ErrInvalid) {
			return "", err
		}
		return "", problem("invalid_composition", "invalid composition inputs or output exceeds 1 MiB")
	}
	if resolved.Literal == nil {
		return "", problem("state_corrupt", "unresolved composition")
	}
	return *resolved.Literal, nil
}

func resolvedArguments(args []v1.Argument, id string, snap plan.Snapshot, result *v1.ExecutionResult, entry *execution, runtime v1.Runtime) ([]string, error) {
	values := make([]string, len(args))
	for i, arg := range args {
		if err := arg.Walk(func(v v1.Value) error {
			if v.Secret {
				return problem("invalid_contract", "sensitive values are not permitted in argv")
			}
			return nil
		}); err != nil {
			return nil, err
		}
		var err error
		values[i], err = resolvedValue(plannedValue(arg.Value), "", id, snap, result, entry, runtime)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func walkPlanned(v v1.PlannedValue, visit func(v1.Value) error) error {
	if v.Composition != nil {
		return v.Composition.Walk(visit)
	}
	return visit(v1.Value{Literal: v.Literal, Ref: v.Symbolic})
}

func publicationTarget(id string, snap plan.Snapshot, result *v1.ExecutionResult, entry *execution) (string, error) {
	value := plannedValue(*snap.Plan.Publish.Probe.Target)
	if snap.Plan.Publish.Probe.Target.Secret {
		value = snap.Secrets["publish/readiness"]
	}
	target, err := resolvedValue(value, "", id, snap, result, entry, v1.Native)
	if err != nil {
		return "", err
	}
	expected, e := url.Parse(result.Origin)
	actual, a := url.Parse(target)
	if e != nil || a != nil || actual.Scheme != expected.Scheme || actual.Host != expected.Host || actual.User != nil {
		return "", problem("invalid_publication", "aggregate target must retain canonical origin authority")
	}
	return target, nil
}

func validateProbeDestination(kind, target string) error {
	port := ""
	if kind == "http" {
		u, err := url.Parse(target)
		if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.HasSuffix(u.Host, ":") {
			return problem("invalid_probe", "invalid HTTP readiness destination")
		}
		port = u.Port()
		if port == "" {
			return nil
		}
	} else {
		if !strings.Contains(target, ":") {
			port = target
		} else {
			_, p, err := net.SplitHostPort(target)
			if err != nil {
				return problem("invalid_probe", "invalid TCP readiness destination")
			}
			port = p
		}
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return problem("invalid_probe", "readiness destination requires a numeric port in 1..65535")
	}
	return nil
}

func canonicalArguments(args []v1.Argument) []v1.Argument {
	if len(args) == 0 {
		return nil
	}
	result := make([]v1.Argument, len(args))
	for i, arg := range args {
		result[i] = v1.Argument{Value: arg.Value}
	}
	return result
}
