/*
Copyright 2026 The Crossplane Authors.

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
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/packages/packagestest"

	"github.com/crossplane/crossplane-tools/internal/comments"
	xptypes "github.com/crossplane/crossplane-tools/internal/types"
)

const (
	kindRefSource = `
package kindref

type Reference struct {
	APIVersion string
	Kind       string
	Name       string
}

type Selector struct {
	APIVersion string
	Kind       string
}

func NewReference(apiVersion, kind string, r any) *Reference { return nil }
`

	multiKindSource = `
package v1alpha1

import "golang.org/fake/kindref"

type GrantParameters struct {
	// +crossplane:generate:reference:type=HumanUser
	// +crossplane:generate:reference:apiVersion=user.example.org/v1alpha1
	// +crossplane:generate:reference:type=MachineUser
	// +crossplane:generate:reference:apiVersion=user.example.org/v1alpha1
	UserID *string

	UserIDRef *kindref.Reference

	UserIDSelector *kindref.Selector

	// +crossplane:generate:reference:type=github.com/example/provider/apis/project/v1alpha1.Grant
	// +crossplane:generate:reference:apiVersion=project.example.org/v1alpha1
	// +crossplane:generate:reference:extractor=github.com/example/provider/apis/project/v1alpha1.GrantID()
	// +crossplane:generate:reference:type=Grant
	// +crossplane:generate:reference:apiVersion=user.example.org/v1alpha1
	// +crossplane:generate:reference:extractor=github.com/crossplane/crossplane-runtime/v2/pkg/reference.ExternalName()
	// +crossplane:generate:reference:type=MachineUser
	// +crossplane:generate:reference:apiVersion=user.example.org/v1alpha1
	// +crossplane:generate:reference:extractor=github.com/crossplane/crossplane-runtime/v2/pkg/reference.ExternalName()
	// +crossplane:generate:reference:refFieldName=GrantRef
	// +crossplane:generate:reference:selectorFieldName=GrantSelector
	GrantID string

	GrantRef *kindref.Reference

	GrantSelector *kindref.Selector
}

type GrantSpec struct {
	ForProvider GrantParameters
}

type Grant struct {
	Spec GrantSpec
}
`
)

func TestNewResolveReferencesMultiKind(t *testing.T) {
	exported := packagestest.Export(t, packagestest.Modules, []packagestest.Module{{
		Name: "golang.org/fake",
		Files: map[string]any{
			"v1alpha1/grant.go":  multiKindSource,
			"kindref/kindref.go": kindRefSource,
		},
	}})
	defer exported.Cleanup()
	exported.Config.Mode = packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax
	pkgs, err := packages.Load(exported.Config, fmt.Sprintf("file=%s", exported.File("golang.org/fake", "v1alpha1/grant.go")))
	if err != nil {
		t.Fatal(err)
	}
	f := jen.NewFilePath("golang.org/fake/v1alpha1")
	NewResolveReferencesV2(xptypes.NewTraverser(comments.In(pkgs[0])), "mg", "example.org/client", "example.org/reference")(f, pkgs[0].Types.Scope().Lookup("Grant"))
	got := fmt.Sprintf("%#v", f)
	if diff := cmp.Diff(generatedMultiKind, got); diff != "" {
		t.Errorf("NewResolveReferencesV2(): -want, +got\n%s", diff)
	}
}

const generatedMultiKind = `package v1alpha1

import (
	"context"
	client "example.org/client"
	reference "example.org/reference"
	reference1 "github.com/crossplane/crossplane-runtime/v2/pkg/reference"
	v1alpha1 "github.com/example/provider/apis/project/v1alpha1"
	errors "github.com/pkg/errors"
	kindref "golang.org/fake/kindref"
)

// ResolveReferences of this Grant.
func (mg *Grant) ResolveReferences(ctx context.Context, c client.Reader) error {
	r := reference.NewAPINamespacedResolver(c, mg)

	var rsp reference.NamespacedResolutionResponse
	var err error

	{
		ref := mg.Spec.ForProvider.UserIDRef.ToReference()
		sel := mg.Spec.ForProvider.UserIDSelector.ToSelector()
		apiVersion, kind := mg.Spec.ForProvider.UserIDRef.GetAPIVersion(), mg.Spec.ForProvider.UserIDRef.GetKind()
		// The selector chooses the target when it will select a new reference.
		if sel != nil && (ref == nil || sel.Policy.IsResolvePolicyAlways()) {
			apiVersion, kind = mg.Spec.ForProvider.UserIDSelector.GetAPIVersion(), mg.Spec.ForProvider.UserIDSelector.GetKind()
		}
		switch {
		case apiVersion == "" && kind == "", apiVersion == "" && kind == "HumanUser", apiVersion == "user.example.org/v1alpha1" && kind == "HumanUser":
			rsp, err = r.Resolve(ctx, reference.NamespacedResolutionRequest{
				CurrentValue: reference.FromPtrValue(mg.Spec.ForProvider.UserID),
				Extract:      reference.ExternalName(),
				Namespace:    mg.GetNamespace(),
				Reference:    ref,
				Selector:     sel,
				To: reference.To{
					List:    &HumanUserList{},
					Managed: &HumanUser{},
				},
			})
		case apiVersion == "" && kind == "MachineUser", apiVersion == "user.example.org/v1alpha1" && kind == "MachineUser":
			rsp, err = r.Resolve(ctx, reference.NamespacedResolutionRequest{
				CurrentValue: reference.FromPtrValue(mg.Spec.ForProvider.UserID),
				Extract:      reference.ExternalName(),
				Namespace:    mg.GetNamespace(),
				Reference:    ref,
				Selector:     sel,
				To: reference.To{
					List:    &MachineUserList{},
					Managed: &MachineUser{},
				},
			})
		default:
			err = errors.Errorf("unsupported reference target apiVersion %q, kind %q: must be one of: user.example.org/v1alpha1 HumanUser (default), user.example.org/v1alpha1 MachineUser", apiVersion, kind)
		}
		if err != nil {
			return errors.Wrap(err, "mg.Spec.ForProvider.UserID")
		}
		mg.Spec.ForProvider.UserID = reference.ToPtrValue(rsp.ResolvedValue)
		mg.Spec.ForProvider.UserIDRef = kindref.NewReference(apiVersion, kind, rsp.ResolvedReference)
	}

	{
		ref := mg.Spec.ForProvider.GrantRef.ToReference()
		sel := mg.Spec.ForProvider.GrantSelector.ToSelector()
		apiVersion, kind := mg.Spec.ForProvider.GrantRef.GetAPIVersion(), mg.Spec.ForProvider.GrantRef.GetKind()
		// The selector chooses the target when it will select a new reference.
		if sel != nil && (ref == nil || sel.Policy.IsResolvePolicyAlways()) {
			apiVersion, kind = mg.Spec.ForProvider.GrantSelector.GetAPIVersion(), mg.Spec.ForProvider.GrantSelector.GetKind()
		}
		switch {
		case apiVersion == "" && kind == "", apiVersion == "project.example.org/v1alpha1" && kind == "Grant":
			rsp, err = r.Resolve(ctx, reference.NamespacedResolutionRequest{
				CurrentValue: mg.Spec.ForProvider.GrantID,
				Extract:      v1alpha1.GrantID(),
				Namespace:    mg.GetNamespace(),
				Reference:    ref,
				Selector:     sel,
				To: reference.To{
					List:    &v1alpha1.GrantList{},
					Managed: &v1alpha1.Grant{},
				},
			})
		case apiVersion == "user.example.org/v1alpha1" && kind == "Grant":
			rsp, err = r.Resolve(ctx, reference.NamespacedResolutionRequest{
				CurrentValue: mg.Spec.ForProvider.GrantID,
				Extract:      reference1.ExternalName(),
				Namespace:    mg.GetNamespace(),
				Reference:    ref,
				Selector:     sel,
				To: reference.To{
					List:    &GrantList{},
					Managed: &Grant{},
				},
			})
		case apiVersion == "" && kind == "MachineUser", apiVersion == "user.example.org/v1alpha1" && kind == "MachineUser":
			rsp, err = r.Resolve(ctx, reference.NamespacedResolutionRequest{
				CurrentValue: mg.Spec.ForProvider.GrantID,
				Extract:      reference1.ExternalName(),
				Namespace:    mg.GetNamespace(),
				Reference:    ref,
				Selector:     sel,
				To: reference.To{
					List:    &MachineUserList{},
					Managed: &MachineUser{},
				},
			})
		case apiVersion == "" && kind == "Grant":
			err = errors.New("kind \"Grant\" matches more than one reference target, set apiVersion to one of: project.example.org/v1alpha1, user.example.org/v1alpha1")
		default:
			err = errors.Errorf("unsupported reference target apiVersion %q, kind %q: must be one of: project.example.org/v1alpha1 Grant (default), user.example.org/v1alpha1 Grant, user.example.org/v1alpha1 MachineUser", apiVersion, kind)
		}
		if err != nil {
			return errors.Wrap(err, "mg.Spec.ForProvider.GrantID")
		}
		mg.Spec.ForProvider.GrantID = rsp.ResolvedValue
		mg.Spec.ForProvider.GrantRef = kindref.NewReference(apiVersion, kind, rsp.ResolvedReference)
	}

	return nil
}
`

func TestReferenceProcessorMultiKindErrors(t *testing.T) {
	const header = `
package v1alpha1

import "golang.org/fake/kindref"

type NoConstructor struct{}

type Parameters struct {
`
	const footer = `
	UserIDRef *kindref.Reference
	UserIDsRefs []kindref.Reference
	UserIDSelector *kindref.Selector
	OtherRef *NoConstructor
}
`
	cases := map[string]struct {
		field string
		want  string
	}{
		"APIVersionCount": {
			field: `
	// +crossplane:generate:reference:type=HumanUser
	// +crossplane:generate:reference:type=MachineUser
	// +crossplane:generate:reference:apiVersion=user.example.org/v1
	UserID *string`,
			want: "cannot process the multi-kind reference of field UserID: got 1 apiVersion markers for 2 type markers, want one per type",
		},
		"ExtractorCount": {
			field: `
	// +crossplane:generate:reference:type=HumanUser
	// +crossplane:generate:reference:apiVersion=user.example.org/v1
	// +crossplane:generate:reference:extractor=A()
	// +crossplane:generate:reference:type=MachineUser
	// +crossplane:generate:reference:apiVersion=user.example.org/v1
	// +crossplane:generate:reference:type=Robot
	// +crossplane:generate:reference:apiVersion=user.example.org/v1
	// +crossplane:generate:reference:extractor=B()
	UserID *string`,
			want: "cannot process the multi-kind reference of field UserID: got 2 extractor markers for 3 type markers, want none or one per type",
		},
		"MalformedAPIVersion": {
			field: `
	// +crossplane:generate:reference:type=HumanUser
	// +crossplane:generate:reference:apiVersion=v1
	UserID *string`,
			want: `cannot process the multi-kind reference of field UserID: apiVersion "v1" of type HumanUser is not of the form group/version`,
		},
		"DuplicateTarget": {
			field: `
	// +crossplane:generate:reference:type=HumanUser
	// +crossplane:generate:reference:apiVersion=user.example.org/v1
	// +crossplane:generate:reference:type=example.org/other.HumanUser
	// +crossplane:generate:reference:apiVersion=user.example.org/v1
	UserID *string`,
			want: "cannot process the multi-kind reference of field UserID: user.example.org/v1, Kind=HumanUser is configured more than once",
		},
		"SliceField": {
			field: `
	// +crossplane:generate:reference:type=HumanUser
	// +crossplane:generate:reference:apiVersion=user.example.org/v1
	// +crossplane:generate:reference:refFieldName=UserIDsRefs
	UserIDs []string`,
			want: "cannot process the multi-kind reference of field UserIDs: multi-kind references are not supported for slice fields",
		},
		"NoConstructor": {
			field: `
	// +crossplane:generate:reference:type=HumanUser
	// +crossplane:generate:reference:apiVersion=user.example.org/v1
	// +crossplane:generate:reference:refFieldName=OtherRef
	Other *string`,
			want: "cannot process the multi-kind reference of field Other: package golang.org/fake/v1alpha1 must have a function NewNoConstructor that returns the reference to store for a resolved reference",
		},
		"NoReferenceField": {
			field: `
	// +crossplane:generate:reference:type=HumanUser
	// +crossplane:generate:reference:apiVersion=user.example.org/v1
	Missing *string`,
			want: "cannot process the multi-kind reference of field Missing: Parameters has no reference field MissingRef",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			exported := packagestest.Export(t, packagestest.Modules, []packagestest.Module{{
				Name: "golang.org/fake",
				Files: map[string]any{
					"v1alpha1/params.go": header + tc.field + footer,
					"kindref/kindref.go": kindRefSource,
				},
			}})
			defer exported.Cleanup()
			exported.Config.Mode = packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax
			pkgs, err := packages.Load(exported.Config, fmt.Sprintf("file=%s", exported.File("golang.org/fake", "v1alpha1/params.go")))
			if err != nil {
				t.Fatal(err)
			}
			n := pkgs[0].Types.Scope().Lookup("Parameters").Type().(*types.Named)
			st := n.Underlying().(*types.Struct)
			c := comments.In(pkgs[0])
			rp := NewReferenceProcessor("mg", WithDefaultExtractor(jen.Id("ExternalName").Call()))
			var got error
			for f := range st.Fields() {
				if got = rp.Process(n, f, "", c.For(f)); got != nil {
					break
				}
			}
			if got == nil {
				t.Fatalf("Process(): want error %q, got nil", tc.want)
			}
			if diff := cmp.Diff(tc.want, got.Error()); diff != "" {
				t.Errorf("Process(): -want error, +got error:\n%s", diff)
			}
		})
	}
}
