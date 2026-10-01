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
	"fmt"
	"go/types"
	"strings"

	"github.com/dave/jennifer/jen"
	"github.com/pkg/errors"

	xptypes "github.com/crossplane/crossplane-tools/internal/types"
)

const (
	funcnameNewAPINamespacedResolver          = "NewAPINamespacedResolver"
	typenameNamespacedResolutionRequest       = "NamespacedResolutionRequest"
	typenameNamespacedResolutionResponse      = "NamespacedResolutionResponse"
	typenameMultiNamespacedResolutionRequest  = "MultiNamespacedResolutionRequest"
	typenameMultiNamespacedResolutionResponse = "MultiNamespacedResolutionResponse"
)

const (
	funcnameNewAPIResolver          = "NewAPIResolver"
	typenameResolutionRequest       = "ResolutionRequest"
	typenameResolutionResponse      = "ResolutionResponse"
	typenameMultiResolutionRequest  = "MultiResolutionRequest"
	typenameMultiResolutionResponse = "MultiResolutionResponse"
)

type names struct {
	APIResolverFunctionName         string
	ResolutionRequestTypeName       string
	ResolutionResponseTypeName      string
	MultiResolutionRequestTypeName  string
	MultiResolutionResponseTypeName string
}

// NewResolveReferences returns a NewMethod that writes a ResolveReferences for
// given managed resource, if needed.
func NewResolveReferences(traverser *xptypes.Traverser, receiver, clientPath, referencePkgPath string) New {
	return NewResolveReferencesCommon(traverser, receiver, clientPath, referencePkgPath, names{
		APIResolverFunctionName:         funcnameNewAPIResolver,
		ResolutionRequestTypeName:       typenameResolutionRequest,
		ResolutionResponseTypeName:      typenameResolutionResponse,
		MultiResolutionRequestTypeName:  typenameMultiResolutionRequest,
		MultiResolutionResponseTypeName: typenameMultiResolutionResponse,
	})
}

// NewResolveReferencesV2 returns a NewMethod that writes a ResolveReferences for
// given managed resource, if needed.
func NewResolveReferencesV2(traverser *xptypes.Traverser, receiver, clientPath, referencePkgPath string) New {
	return NewResolveReferencesCommon(traverser, receiver, clientPath, referencePkgPath, names{
		APIResolverFunctionName:         funcnameNewAPINamespacedResolver,
		ResolutionRequestTypeName:       typenameNamespacedResolutionRequest,
		ResolutionResponseTypeName:      typenameNamespacedResolutionResponse,
		MultiResolutionRequestTypeName:  typenameMultiNamespacedResolutionRequest,
		MultiResolutionResponseTypeName: typenameMultiNamespacedResolutionResponse,
	})
}

// NewResolveReferencesCommon returns a NewMethod that writes a ResolveReferences for
// given managed resource, if needed.
func NewResolveReferencesCommon(traverser *xptypes.Traverser, receiver, clientPath, referencePkgPath string, names names) New {
	return func(f *jen.File, o types.Object) {
		n, ok := o.Type().(*types.Named)
		if !ok {
			return
		}
		refProcessor := NewReferenceProcessor(receiver,
			WithDefaultExtractor(jen.Qual(referencePkgPath, "ExternalName").Call()),
		)
		cfg := &xptypes.ProcessorConfig{
			Field: refProcessor,
			Named: xptypes.NamedProcessorChain{},
		}
		if err := traverser.Traverse(n, cfg); err != nil {
			panic(errors.Wrapf(err, "cannot traverse the type tree of %s", n.Obj().Name()))
		}
		refs := refProcessor.GetReferences()
		if len(refs) == 0 {
			return
		}
		hasMultiResolution := false
		hasSingleResolution := false
		resolverCalls := make(jen.Statement, len(refs))
		for i, ref := range refs {
			if ref.IsSlice {
				hasMultiResolution = true
				resolverCalls[i] = encapsulate(0, multiResolutionCall(ref, referencePkgPath, names.MultiResolutionRequestTypeName), ref.GoValueFieldPath...).Line()
			} else if len(ref.Targets) > 0 {
				hasSingleResolution = true
				resolverCalls[i] = encapsulate(0, multiKindResolutionCall(ref, referencePkgPath, names.ResolutionRequestTypeName), ref.GoValueFieldPath...).Line()
			} else {
				hasSingleResolution = true
				resolverCalls[i] = encapsulate(0, singleResolutionCall(ref, referencePkgPath, names.ResolutionRequestTypeName), ref.GoValueFieldPath...).Line()
			}
		}
		var initStatements jen.Statement
		if hasSingleResolution {
			initStatements = append(initStatements, jen.Var().Id("rsp").Qual(referencePkgPath, names.ResolutionResponseTypeName))
		}
		if hasMultiResolution {
			initStatements = append(initStatements, jen.Line().Var().Id("mrsp").Qual(referencePkgPath, names.MultiResolutionResponseTypeName))
		}

		f.Commentf("ResolveReferences of this %s.", o.Name())
		f.Func().Params(jen.Id(receiver).Op("*").Id(o.Name())).Id("ResolveReferences").Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("c").Qual(clientPath, "Reader")).Error().Block(
			jen.Id("r").Op(":=").Qual(referencePkgPath, names.APIResolverFunctionName).Call(jen.Id("c"), jen.Id(receiver)),
			jen.Line(),
			&initStatements,
			jen.Var().Err().Error(),
			jen.Line(),
			&resolverCalls,
			jen.Line(),
			jen.Return(jen.Nil()),
		)
	}
}

type resolutionCallFn func(parentFields ...string) *jen.Statement

// encapsulate goes through the fields and encapsulates the final call with nil
// guard and/or for loops.
func encapsulate(index int, callFn resolutionCallFn, fields ...string) *jen.Statement {
	cleaner := strings.NewReplacer("[]", "", "*", "")
	if len(fields) <= index {
		return callFn(fields...)
	}
	field := fields[index]
	fieldPath := jen.Id(cleaner.Replace(fields[0]))
	for i := 1; i <= index; i++ {
		fieldPath = fieldPath.Dot(cleaner.Replace(fields[i]))
	}
	switch {
	case strings.HasPrefix(field, "*"):
		fields[index] = cleaner.Replace(fields[index])
		return jen.If(fieldPath.Op("!=").Nil()).Block(encapsulate(index+1, callFn, fields...))
	case strings.HasPrefix(field, "[]"):
		fields[index] = cleaner.Replace(fields[index]) + fmt.Sprintf("[i%d]", index)
		return jen.For(
			jen.Id(fmt.Sprintf("i%d", index)).Op(":=").Lit(0),
			jen.Id(fmt.Sprintf("i%d", index)).Op("<").Len(fieldPath),
			jen.Id(fmt.Sprintf("i%d", index)).Op("++"),
		).Block(encapsulate(index+1, callFn, fields...))
	default:
		return encapsulate(index+1, callFn, fields...)
	}
}

func singleResolutionCall(ref Reference, referencePkgPath, resolutionRequestTypeName string) resolutionCallFn {
	return func(fields ...string) *jen.Statement {
		prefixPath := jen.Id(fields[0])
		for i := 1; i < len(fields)-1; i++ {
			prefixPath = prefixPath.Dot(fields[i])
		}
		currentValuePath := prefixPath.Clone().Dot(fields[len(fields)-1])
		referenceFieldPath := prefixPath.Clone().Dot(ref.GoRefFieldName)
		selectorFieldPath := prefixPath.Clone().Dot(ref.GoSelectorFieldName)

		setResolvedValue := currentValuePath.Clone().Op("=").Id("rsp").Dot("ResolvedValue")
		toPointerFunction := "ToPtrValue"
		fromPointerFunction := "FromPtrValue"
		if ref.IsFloatPointer {
			toPointerFunction = "ToFloatPtrValue"
			fromPointerFunction = "FromFloatPtrValue"
		}
		if ref.IsPointer {
			setResolvedValue = currentValuePath.Clone().Op("=").Qual(referencePkgPath, toPointerFunction).Call(jen.Id("rsp").Dot("ResolvedValue"))
			currentValuePath = jen.Qual(referencePkgPath, fromPointerFunction).Call(currentValuePath)
		}
		return &jen.Statement{
			jen.List(jen.Id("rsp"), jen.Err()).Op("=").Id("r").Dot("Resolve").Call(
				jen.Id("ctx"),
				jen.Qual(referencePkgPath, resolutionRequestTypeName).Values(jen.Dict{
					jen.Id("CurrentValue"): currentValuePath,
					jen.Id("Reference"):    referenceFieldPath,
					jen.Id("Selector"):     selectorFieldPath,
					jen.Id("To"): jen.Qual(referencePkgPath, "To").Values(jen.Dict{
						jen.Id("Managed"): ref.RemoteType,
						jen.Id("List"):    ref.RemoteListType,
					}),
					jen.Id("Extract"):   ref.Extractor,
					jen.Id("Namespace"): ref.GetNamespace,
				},
				),
			),
			jen.Line(),
			jen.If(jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Qual("github.com/pkg/errors", "Wrap").Call(jen.Err(), jen.Lit(strings.Join(ref.GoValueFieldPath, ".")))),
			),
			jen.Line(),
			setResolvedValue,
			jen.Line(),
			referenceFieldPath.Clone().Op("=").Id("rsp").Dot("ResolvedReference"),
			jen.Line(),
		}
	}
}

// multiKindResolutionCall resolves a reference whose reference and selector
// choose one of several targets by apiVersion and kind. Only the configured
// targets are ever looked up. The generated code:
//
//   - takes the apiVersion and kind from the selector if it will select a new
//     reference (as reference.ResolutionRequest.IsNoOp decides), and from the
//     reference otherwise,
//   - resolves the default target if both are empty, the target with the
//     kind if the kind alone is unique among the targets, or the target with
//     both the apiVersion and kind,
//   - fails with an error listing the targets otherwise, and
//   - stores the resolved reference with the apiVersion and kind it was
//     resolved with.
func multiKindResolutionCall(ref Reference, referencePkgPath, resolutionRequestTypeName string) resolutionCallFn { //nolint:gocyclo // Generates a lot of code, but simple to follow.
	return func(fields ...string) *jen.Statement {
		prefixPath := jen.Id(fields[0])
		for i := 1; i < len(fields)-1; i++ {
			prefixPath = prefixPath.Dot(fields[i])
		}
		currentValuePath := prefixPath.Clone().Dot(fields[len(fields)-1])
		referenceFieldPath := prefixPath.Clone().Dot(ref.GoRefFieldName)
		selectorFieldPath := prefixPath.Clone().Dot(ref.GoSelectorFieldName)

		setResolvedValue := currentValuePath.Clone().Op("=").Id("rsp").Dot("ResolvedValue")
		toPointerFunction := "ToPtrValue"
		fromPointerFunction := "FromPtrValue"
		if ref.IsFloatPointer {
			toPointerFunction = "ToFloatPtrValue"
			fromPointerFunction = "FromFloatPtrValue"
		}
		if ref.IsPointer {
			setResolvedValue = currentValuePath.Clone().Op("=").Qual(referencePkgPath, toPointerFunction).Call(jen.Id("rsp").Dot("ResolvedValue"))
			currentValuePath = jen.Qual(referencePkgPath, fromPointerFunction).Call(currentValuePath)
		}

		kindCount := map[string]int{}
		for _, t := range ref.Targets {
			kindCount[t.Kind]++
		}
		is := func(apiVersion, kind string) *jen.Statement {
			return jen.Id("apiVersion").Op("==").Lit(apiVersion).Op("&&").Id("kind").Op("==").Lit(kind)
		}
		cases := make([]jen.Code, 0, len(ref.Targets)+2)
		descs := make([]string, len(ref.Targets))
		ambiguous := map[string][]string{}
		var ambiguousKinds []string
		for i, t := range ref.Targets {
			var conds []jen.Code
			descs[i] = t.APIVersion + " " + t.Kind
			if i == 0 {
				descs[i] += " (default)"
				conds = append(conds, is("", ""))
			}
			if kindCount[t.Kind] == 1 {
				conds = append(conds, is("", t.Kind))
			} else {
				if _, ok := ambiguous[t.Kind]; !ok {
					ambiguousKinds = append(ambiguousKinds, t.Kind)
				}
				ambiguous[t.Kind] = append(ambiguous[t.Kind], t.APIVersion)
			}
			conds = append(conds, is(t.APIVersion, t.Kind))
			cases = append(cases, jen.Case(conds...).Block(
				jen.List(jen.Id("rsp"), jen.Err()).Op("=").Id("r").Dot("Resolve").Call(
					jen.Id("ctx"),
					jen.Qual(referencePkgPath, resolutionRequestTypeName).Values(jen.Dict{
						jen.Id("CurrentValue"): currentValuePath.Clone(),
						jen.Id("Reference"):    jen.Id("ref"),
						jen.Id("Selector"):     jen.Id("sel"),
						jen.Id("To"): jen.Qual(referencePkgPath, "To").Values(jen.Dict{
							jen.Id("Managed"): t.RemoteType,
							jen.Id("List"):    t.RemoteListType,
						}),
						jen.Id("Extract"):   t.Extractor,
						jen.Id("Namespace"): ref.GetNamespace,
					}),
				),
			))
		}
		for _, k := range ambiguousKinds {
			cases = append(cases, jen.Case(is("", k)).Block(
				jen.Err().Op("=").Qual("github.com/pkg/errors", "New").Call(jen.Lit(fmt.Sprintf(
					"kind %q matches more than one reference target, set apiVersion to one of: %s", k, strings.Join(ambiguous[k], ", ")))),
			))
		}
		cases = append(cases, jen.Default().Block(
			jen.Err().Op("=").Qual("github.com/pkg/errors", "Errorf").Call(
				jen.Lit("unsupported reference target apiVersion %q, kind %q: must be one of: "+strings.Join(descs, ", ")),
				jen.Id("apiVersion"), jen.Id("kind")),
		))

		return &jen.Statement{
			jen.Block(
				jen.Id("ref").Op(":=").Add(referenceFieldPath.Clone()).Dot("ToReference").Call(),
				jen.Id("sel").Op(":=").Add(selectorFieldPath.Clone()).Dot("ToSelector").Call(),
				jen.List(jen.Id("apiVersion"), jen.Id("kind")).Op(":=").List(
					referenceFieldPath.Clone().Dot("GetAPIVersion").Call(),
					referenceFieldPath.Clone().Dot("GetKind").Call(),
				),
				jen.Comment("The selector chooses the target when it will select a new reference."),
				jen.If(jen.Id("sel").Op("!=").Nil().Op("&&").Parens(
					jen.Id("ref").Op("==").Nil().Op("||").Id("sel").Dot("Policy").Dot("IsResolvePolicyAlways").Call(),
				)).Block(
					jen.List(jen.Id("apiVersion"), jen.Id("kind")).Op("=").List(
						selectorFieldPath.Clone().Dot("GetAPIVersion").Call(),
						selectorFieldPath.Clone().Dot("GetKind").Call(),
					),
				),
				jen.Switch().Block(cases...),
				jen.If(jen.Err().Op("!=").Nil()).Block(
					jen.Return(jen.Qual("github.com/pkg/errors", "Wrap").Call(jen.Err(), jen.Lit(strings.Join(ref.GoValueFieldPath, ".")))),
				),
				setResolvedValue,
				referenceFieldPath.Clone().Op("=").Add(ref.NewReference.Clone()).Call(jen.Id("apiVersion"), jen.Id("kind"), jen.Id("rsp").Dot("ResolvedReference")),
			),
			jen.Line(),
		}
	}
}

func multiResolutionCall(ref Reference, referencePkgPath, multiResolutionRequestTypeName string) resolutionCallFn {
	return func(fields ...string) *jen.Statement {
		prefixPath := jen.Id(fields[0])
		for i := 1; i < len(fields)-1; i++ {
			prefixPath = prefixPath.Dot(fields[i])
		}
		currentValuePath := prefixPath.Clone().Dot(fields[len(fields)-1])
		referenceFieldPath := prefixPath.Clone().Dot(ref.GoRefFieldName)
		selectorFieldPath := prefixPath.Clone().Dot(ref.GoSelectorFieldName)

		setResolvedValues := currentValuePath.Clone().Op("=").Id("mrsp").Dot("ResolvedValues")
		toPointersFunction := "ToPtrValues"
		fromPointersFunction := "FromPtrValues"
		if ref.IsFloatPointer {
			toPointersFunction = "ToFloatPtrValues"
			fromPointersFunction = "FromFloatPtrValues"
		}

		if ref.IsPointer {
			setResolvedValues = currentValuePath.Clone().Op("=").Qual(referencePkgPath, toPointersFunction).Call(jen.Id("mrsp").Dot("ResolvedValues"))
			currentValuePath = jen.Qual(referencePkgPath, fromPointersFunction).Call(currentValuePath)
		}

		return &jen.Statement{
			jen.List(jen.Id("mrsp"), jen.Err()).Op("=").Id("r").Dot("ResolveMultiple").Call(
				jen.Id("ctx"),
				jen.Qual(referencePkgPath, multiResolutionRequestTypeName).Values(jen.Dict{
					jen.Id("CurrentValues"): currentValuePath,
					jen.Id("References"):    referenceFieldPath,
					jen.Id("Selector"):      selectorFieldPath,
					jen.Id("To"): jen.Qual(referencePkgPath, "To").Values(jen.Dict{
						jen.Id("Managed"): ref.RemoteType,
						jen.Id("List"):    ref.RemoteListType,
					}),
					jen.Id("Extract"):   ref.Extractor,
					jen.Id("Namespace"): ref.GetNamespace,
				},
				),
			),
			jen.Line(),
			jen.If(jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Qual("github.com/pkg/errors", "Wrap").Call(jen.Err(), jen.Lit(strings.Join(ref.GoValueFieldPath, ".")))),
			),
			jen.Line(),
			setResolvedValues,
			jen.Line(),
			referenceFieldPath.Clone().Op("=").Id("mrsp").Dot("ResolvedReferences"),
			jen.Line(),
		}
	}
}
