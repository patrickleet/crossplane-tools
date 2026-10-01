/*
Copyright 2021 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package method

import (
	"go/types"
	"regexp"
	"strings"

	"github.com/dave/jennifer/jen"
	"github.com/pkg/errors"

	"github.com/crossplane/crossplane-tools/internal/comments"
)

// Comment markers used by ReferenceProcessor.
const (
	ReferenceTypeMarker               = "crossplane:generate:reference:type"
	ReferenceExtractorMarker          = "crossplane:generate:reference:extractor"
	ReferenceReferenceFieldNameMarker = "crossplane:generate:reference:refFieldName"
	ReferenceSelectorFieldNameMarker  = "crossplane:generate:reference:selectorFieldName"
	ReferenceAPIVersionMarker         = "crossplane:generate:reference:apiVersion"
)

var regexFunctionCall = regexp.MustCompile(`((.+)\.)?([^.]+\(.*\))`)

// Reference is the internal representation that has enough information to let
// us generate the resolver.
type Reference struct {
	// RemoteType represents the type whose reference we're holding.
	RemoteType *jen.Statement

	// Extractor is the function call of the function that will take referenced
	// instance and return a string or []string to be set as value.
	Extractor *jen.Statement

	// RemoteListType is the list type of the type whose reference we're holding.
	RemoteListType *jen.Statement

	// GetNamespace is the function call for getting the namespace of instance.
	GetNamespace *jen.Statement

	// GoValueFieldPath is the list of fields that needs to be traveled to access
	// the current value field. It may include prefixes like [] for array fields,
	// * for pointer fields or []* for array of pointer fields.
	GoValueFieldPath []string

	// GoRefFieldName is the name of the field that holds the reference (or slice
	// of references): *xpv1.Reference / []xpv1.Reference for legacy resources, or
	// *xpv2.Reference / *xpv2.NamespacedReference (and their slice forms) for
	// cluster-scoped and namespaced v2 resources respectively.
	GoRefFieldName string

	// GoSelectorFieldName is the name of the field that holds the selector:
	// *xpv1.Selector for legacy resources, or *xpv2.Selector /
	// *xpv2.NamespacedSelector for cluster-scoped and namespaced v2 resources
	// respectively.
	GoSelectorFieldName string

	// IsSlice tells whether the current value type is a slice kind.
	IsSlice bool

	// IsPointer tells whether the current value type is a pointer kind.
	IsPointer bool

	// IsFloatPointer tells whether the current value pointer is of type float64
	IsFloatPointer bool

	// Targets are the targets of a multi-kind reference, whose reference and
	// selector choose a target by apiVersion and kind. The first target is
	// the default. Targets is empty for a single-target reference.
	Targets []ReferenceTarget

	// NewReference is the function that returns the reference to store for
	// a resolved multi-kind reference, given its apiVersion, kind and the
	// resolved reference.
	NewReference *jen.Statement
}

// ReferenceTarget is one of the targets of a multi-kind reference.
type ReferenceTarget struct {
	// APIVersion of the target, as group/version.
	APIVersion string

	// Kind of the target.
	Kind string

	// RemoteType is the type of the target.
	RemoteType *jen.Statement

	// RemoteListType is the list type of the target.
	RemoteListType *jen.Statement

	// Extractor is the function call that returns the value to set from a
	// target instance.
	Extractor *jen.Statement
}

// ReferenceProcessorOption is used to configure ReferenceProcessor.
type ReferenceProcessorOption func(*ReferenceProcessor)

// WithDefaultExtractor returns an option that sets the extractor to given
// call.
func WithDefaultExtractor(ext *jen.Statement) ReferenceProcessorOption {
	return func(rp *ReferenceProcessor) {
		rp.DefaultExtractor = ext
	}
}

// NewReferenceProcessor returns a new *ReferenceProcessor .
func NewReferenceProcessor(receiver string, opts ...ReferenceProcessorOption) *ReferenceProcessor {
	rp := &ReferenceProcessor{
		Receiver: receiver,
	}
	for _, f := range opts {
		f(rp)
	}
	return rp
}

// ReferenceProcessor detects whether the field is marked as referencer and
// composes the internal representation of that reference.
type ReferenceProcessor struct {
	// DefaultExtractor is used when the extractor is not overridden.
	DefaultExtractor *jen.Statement

	// Receiver is prepended to all field paths.
	Receiver string

	refs []Reference
}

// Process stores the reference information of the given field, if any.
//
// A field whose markers include apiVersion markers is a multi-kind reference:
// its type markers list the targets, the first of which is the default, and
// its apiVersion markers list their API versions in the same order. Its
// extractor markers are either absent, or one per target in the same order.
// The reference and selector fields of a multi-kind reference are pointers
// that are nil when unset, and the generated code calls their methods without
// checking for nil, so their types must have:
//
//   - GetAPIVersion and GetKind methods that return "" for a nil receiver,
//   - a ToReference (reference) or ToSelector (selector) method that returns
//     the reference or selector to resolve, and nil for a nil receiver.
//
// The package of the reference field's type must have a New<type name>(
// apiVersion, kind string, resolved) function that returns the reference to
// store. It must return nil when resolved is nil, which happens when
// resolution is a no-op and there was no reference to begin with.
func (rp *ReferenceProcessor) Process(n *types.Named, f *types.Var, _, comment string, parentFields ...string) error { //nolint:gocyclo // Mostly validation; easier to follow in one place.
	markers := comments.ParseMarkers(comment)
	refTypeValues := markers[ReferenceTypeMarker]
	if len(refTypeValues) == 0 {
		return nil
	}
	refType := refTypeValues[0]
	isPointer := false
	isList := false
	isFloatPointer := false
	refFieldName := f.Name() + "Ref"

	// We don't support *[]string.
	switch t := f.Type().(type) {
	// *string|*float64
	case *types.Pointer:
		isPointer = true
	// []string|[]float64.
	case *types.Slice:
		isList = true
		refFieldName = f.Name() + "Refs"
		// []*string|[]*float64
		if _, ok := t.Elem().(*types.Pointer); ok {
			isPointer = true
		}
	}

	if strings.HasSuffix(f.Type().String(), "*float64") {
		isFloatPointer = true
	}

	extractorPath := rp.DefaultExtractor
	if values, ok := markers[ReferenceExtractorMarker]; ok {
		var err error
		extractorPath, err = getFuncCodeFromPath(values[0])
		if err != nil {
			return errors.Wrapf(err, "cannot get extractor function")
		}
	}

	if values, ok := markers[ReferenceReferenceFieldNameMarker]; ok {
		refFieldName = values[0]
	}

	selectorFieldName := f.Name() + "Selector"
	if values, ok := markers[ReferenceSelectorFieldNameMarker]; ok {
		selectorFieldName = values[0]
	}
	var targets []ReferenceTarget
	var newReference *jen.Statement
	if apiVersions, ok := markers[ReferenceAPIVersionMarker]; ok {
		var err error
		if targets, err = getReferenceTargets(refTypeValues, apiVersions, markers[ReferenceExtractorMarker], rp.DefaultExtractor); err != nil {
			return errors.Wrapf(err, "cannot process the multi-kind reference of field %s", f.Name())
		}
		if isList {
			return errors.Errorf("cannot process the multi-kind reference of field %s: multi-kind references are not supported for slice fields", f.Name())
		}
		if newReference, err = getNewReferenceFunc(n, refFieldName); err != nil {
			return errors.Wrapf(err, "cannot process the multi-kind reference of field %s", f.Name())
		}
	}

	path := append([]string{rp.Receiver}, parentFields...)
	rp.refs = append(rp.refs, Reference{
		Targets:             targets,
		NewReference:        newReference,
		RemoteType:          getTypeCodeFromPath(refType),
		RemoteListType:      getTypeCodeFromPath(refType, "List"),
		Extractor:           extractorPath,
		GoValueFieldPath:    append(path, f.Name()),
		GoRefFieldName:      refFieldName,
		GoSelectorFieldName: selectorFieldName,
		GetNamespace:        jen.Id(rp.Receiver).Dot("GetNamespace").Call(),
		IsPointer:           isPointer,
		IsSlice:             isList,
		IsFloatPointer:      isFloatPointer,
	})
	return nil
}

// getReferenceTargets returns the targets of a multi-kind reference from its
// type, apiVersion and extractor marker values.
func getReferenceTargets(refTypes, apiVersions, extractors []string, defaultExtractor *jen.Statement) ([]ReferenceTarget, error) {
	if len(apiVersions) != len(refTypes) {
		return nil, errors.Errorf("got %d apiVersion markers for %d type markers, want one per type", len(apiVersions), len(refTypes))
	}
	if len(extractors) != 0 && len(extractors) != len(refTypes) {
		return nil, errors.Errorf("got %d extractor markers for %d type markers, want none or one per type", len(extractors), len(refTypes))
	}
	targets := make([]ReferenceTarget, len(refTypes))
	seen := map[string]bool{}
	for i, t := range refTypes {
		if gv := strings.Split(apiVersions[i], "/"); len(gv) != 2 || gv[0] == "" || gv[1] == "" {
			return nil, errors.Errorf("apiVersion %q of type %s is not of the form group/version", apiVersions[i], t)
		}
		kind := t[strings.LastIndex(t, ".")+1:]
		id := apiVersions[i] + ", Kind=" + kind
		if seen[id] {
			return nil, errors.Errorf("%s is configured more than once", id)
		}
		seen[id] = true
		extractor := defaultExtractor
		if len(extractors) != 0 {
			var err error
			if extractor, err = getFuncCodeFromPath(extractors[i]); err != nil {
				return nil, errors.Wrapf(err, "cannot get extractor function")
			}
		}
		targets[i] = ReferenceTarget{
			APIVersion:     apiVersions[i],
			Kind:           kind,
			RemoteType:     getTypeCodeFromPath(t),
			RemoteListType: getTypeCodeFromPath(t, "List"),
			Extractor:      extractor,
		}
	}
	return targets, nil
}

// getNewReferenceFunc returns the New<type name> function of the type of the
// supplied reference field of the supplied struct.
func getNewReferenceFunc(n *types.Named, refFieldName string) (*jen.Statement, error) {
	st, ok := n.Underlying().(*types.Struct)
	if !ok {
		return nil, errors.Errorf("%s is not a struct", n.Obj().Name())
	}
	for f := range st.Fields() {
		if f.Name() != refFieldName {
			continue
		}
		t := f.Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		rt, ok := t.(*types.Named)
		if !ok || rt.Obj().Pkg() == nil {
			return nil, errors.Errorf("reference field %s must be of a named type, got %s", refFieldName, f.Type())
		}
		fn := "New" + rt.Obj().Name()
		if _, ok := rt.Obj().Pkg().Scope().Lookup(fn).(*types.Func); !ok {
			return nil, errors.Errorf("package %s must have a function %s that returns the reference to store for a resolved reference", rt.Obj().Pkg().Path(), fn)
		}
		return jen.Qual(rt.Obj().Pkg().Path(), fn), nil
	}
	return nil, errors.Errorf("%s has no reference field %s", n.Obj().Name(), refFieldName)
}

// GetReferences returns all the references accumulated so far from processing.
func (rp *ReferenceProcessor) GetReferences() []Reference {
	return rp.refs
}

func getTypeCodeFromPath(path string, nameSuffix ...string) *jen.Statement {
	words := strings.Split(path, ".")
	if len(words) == 1 {
		return jen.Op("&").Id(path + strings.Join(nameSuffix, "")).Values()
	}
	name := words[len(words)-1] + strings.Join(nameSuffix, "")
	pkg := strings.TrimSuffix(path, "."+words[len(words)-1])
	return jen.Op("&").Qual(pkg, name).Values()
}

func getFuncCodeFromPath(path string) (*jen.Statement, error) {
	parts := regexFunctionCall.FindStringSubmatch(path)
	// we have a total of four groups in the regular expression so if
	// we do not have four parts, then we cannot handle the reference expression
	// Examples paths are:
	// github.com/upbound/upjet/pkg/resource.ExtractParamPath("a.b.c",true)
	// ExtractParamPath("a.b.c",true)
	// ExtractParamPath("a", false)
	// ExtractParamPath()
	if len(parts) != 4 {
		return nil, errors.Errorf("path %q is not a valid function code", path)
	}
	if len(parts[1]) == 0 {
		return jen.Id(parts[3]), nil
	}
	return jen.Qual(parts[2], parts[3]), nil
}
