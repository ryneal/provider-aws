package iam

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/crossplane/provider-aws/apis/identity/v1beta1"
	aws "github.com/crossplane/provider-aws/pkg/clients"
)

var (
	roleARN                  = "some arn"
	description              = "some-description"
	assumeRolePolicyDocument = `{
		"Version": "2012-10-17",
		"Statement": [
		  {
			"Effect": "Allow",
			"Principal": {
			  "Service": "eks.amazonaws.com"
			},
			"Action": "sts:AssumeRole"
		  }
		]
	   }`
	assumeRolePolicyDocument2 = `{
		"Version": "2012-10-17",
		"Statement": [
		  {
			"Effect": "Allow",
			"Principal": {
			  "Service": "eks.amazonaws.com"
			},
			"Action": "sts:AssumeRole",
			"Condition": {
			  "StringEquals": {"foo": "bar"}
			}
		  }
		]
	   }`
	roleID   = "some Id"
	roleName = "some name"
	tagKey   = "key"
	tagValue = "value"
)

func roleParams(m ...func(*v1beta1.IAMRoleParameters)) *v1beta1.IAMRoleParameters {
	o := &v1beta1.IAMRoleParameters{
		Description:              &description,
		AssumeRolePolicyDocument: assumeRolePolicyDocument,
		MaxSessionDuration:       aws.Int64(1),
	}

	for _, f := range m {
		f(o)
	}

	return o
}

func escapedPolicyJSON() *string {
	p, err := aws.CompactAndEscapeJSON(assumeRolePolicyDocument)
	if err == nil {
		return &p
	}
	return nil
}

func role(m ...func(*iam.Role)) *iam.Role {
	o := &iam.Role{
		Description:              &description,
		AssumeRolePolicyDocument: &assumeRolePolicyDocument,
		MaxSessionDuration:       aws.Int64(1),
	}

	for _, f := range m {
		f(o)
	}

	return o
}

func addRoleOutputFields(r *iam.Role) {
	r.Arn = aws.String(roleARN)
	r.RoleId = aws.String(roleID)
}

func roleObservation(m ...func(*v1beta1.IAMRoleExternalStatus)) *v1beta1.IAMRoleExternalStatus {
	o := &v1beta1.IAMRoleExternalStatus{
		ARN:    roleARN,
		RoleID: roleID,
	}

	for _, f := range m {
		f(o)
	}

	return o
}

func TestGenerateCreateRoleInput(t *testing.T) {
	cases := map[string]struct {
		in  v1beta1.IAMRoleParameters
		out iam.CreateRoleInput
	}{
		"FilledInput": {
			in: *roleParams(),
			out: iam.CreateRoleInput{
				RoleName:                 aws.String(roleName),
				Description:              &description,
				AssumeRolePolicyDocument: aws.String(assumeRolePolicyDocument),
				MaxSessionDuration:       aws.Int64(1),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := GenerateCreateRoleInput(roleName, &tc.in)
			if diff := cmp.Diff(r, &tc.out); diff != "" {
				t.Errorf("GenerateNetworkObservation(...): -want, +got:\n%s", diff)
			}
		})
	}
}

func TestGenerateRoleObservation(t *testing.T) {
	cases := map[string]struct {
		in  iam.Role
		out v1beta1.IAMRoleExternalStatus
	}{
		"AllFilled": {
			in:  *role(addRoleOutputFields),
			out: *roleObservation(),
		},
		"NoRoleId": {
			in: *role(addRoleOutputFields, func(r *iam.Role) {
				r.RoleId = nil
			}),
			out: *roleObservation(func(o *v1beta1.IAMRoleExternalStatus) {
				o.RoleID = ""
			}),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := GenerateRoleObservation(tc.in)
			if diff := cmp.Diff(r, tc.out); diff != "" {
				t.Errorf("GenerateNetworkObservation(...): -want, +got:\n%s", diff)
			}
		})
	}
}

func TestLateInitializeRole(t *testing.T) {
	type args struct {
		spec *v1beta1.IAMRoleParameters
		in   iam.Role
	}
	cases := map[string]struct {
		args args
		want *v1beta1.IAMRoleParameters
	}{
		"AllFilledNoDiff": {
			args: args{
				spec: roleParams(),
				in:   *role(),
			},
			want: roleParams(),
		},
		"AllFilledExternalDiff": {
			args: args{
				spec: roleParams(),
				in: *role(func(r *iam.Role) {
					r.CreateDate = &time.Time{}
				}),
			},
			want: roleParams(),
		},
		"PartialFilled": {
			args: args{
				spec: roleParams(func(p *v1beta1.IAMRoleParameters) {
					p.Description = nil
				}),
				in: *role(),
			},
			want: roleParams(func(p *v1beta1.IAMRoleParameters) {
				p.Description = &description
			}),
		},
		"PointerFields": {
			args: args{
				spec: roleParams(),
				in: *role(func(r *iam.Role) {
					r.Tags = []iam.Tag{
						{
							Key:   &tagKey,
							Value: &tagValue,
						},
					}
					r.PermissionsBoundary = &iam.AttachedPermissionsBoundary{
						PermissionsBoundaryArn: &roleARN,
					}
				}),
			},
			want: roleParams(func(p *v1beta1.IAMRoleParameters) {
				p.Tags = []v1beta1.Tag{
					{
						Key:   tagKey,
						Value: tagValue,
					},
				}
				p.PermissionsBoundary = &roleARN
			}),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			LateInitializeRole(tc.args.spec, &tc.args.in)
			if diff := cmp.Diff(tc.args.spec, tc.want); diff != "" {
				t.Errorf("LateInitializeSpec(...): -want, +got:\n%s", diff)
			}
		})
	}
}

func TestIsRoleUpToDate(t *testing.T) {
	type args struct {
		role iam.Role
		p    v1beta1.IAMRoleParameters
	}

	cases := map[string]struct {
		args     args
		want     bool
		wantDiff string
	}{
		"SameFields": {
			args: args{
				role: iam.Role{
					AssumeRolePolicyDocument: escapedPolicyJSON(),
					Description:              &description,
					MaxSessionDuration:       aws.Int64(1),
					Path:                     aws.String("/"),
					Tags: []iam.Tag{{
						Key:   aws.String("key1"),
						Value: aws.String("value1"),
					}},
				},
				p: v1beta1.IAMRoleParameters{
					Description:              &description,
					AssumeRolePolicyDocument: assumeRolePolicyDocument,
					MaxSessionDuration:       aws.Int64(1),
					Path:                     aws.String("/"),
					Tags: []v1beta1.Tag{{
						Key:   "key1",
						Value: "value1",
					}},
				},
			},
			want:     true,
			wantDiff: "",
		},
		"DifferentPolicy": {
			args: args{
				role: iam.Role{
					AssumeRolePolicyDocument: escapedPolicyJSON(),
					Description:              &description,
					MaxSessionDuration:       aws.Int64(1),
					Path:                     aws.String("/"),
				},
				p: v1beta1.IAMRoleParameters{
					Description:              &description,
					AssumeRolePolicyDocument: assumeRolePolicyDocument2,
					MaxSessionDuration:       aws.Int64(1),
					Path:                     aws.String("/"),
				},
			},
			want: false,
			wantDiff: `Found observed difference in IAM role

desired assume role policy: %7B%22Version%22%3A%222012-10-17%22%2C%22Statement%22%3A%5B%7B%22Effect%22%3A%22Allow%22%2C%22Principal%22%3A%7B%22Service%22%3A%22eks.amazonaws.com%22%7D%2C%22Action%22%3A%22sts%3AAssumeRole%22%2C%22Condition%22%3A%7B%22StringEquals%22%3A%7B%22foo%22%3A%22bar%22%7D%7D%7D%5D%7D
observed assume role policy: %7B%22Version%22%3A%222012-10-17%22%2C%22Statement%22%3A%5B%7B%22Effect%22%3A%22Allow%22%2C%22Principal%22%3A%7B%22Service%22%3A%22eks.amazonaws.com%22%7D%2C%22Action%22%3A%22sts%3AAssumeRole%22%7D%5D%7D`,
		},
		"DifferentFields": {
			args: args{
				role: iam.Role{
					AssumeRolePolicyDocument: &assumeRolePolicyDocument,
					Description:              &description,
					MaxSessionDuration:       aws.Int64(1),
					Path:                     aws.String("//"),
					Tags: []iam.Tag{{
						Key:   aws.String("key1"),
						Value: aws.String("value1"),
					}},
				},
				p: v1beta1.IAMRoleParameters{
					Description:              &description,
					AssumeRolePolicyDocument: assumeRolePolicyDocument,
					MaxSessionDuration:       aws.Int64(1),
					Path:                     aws.String("/"),
					Tags: []v1beta1.Tag{{
						Key:   "key1",
						Value: "value1",
					}},
				},
			},
			want:     false,
			wantDiff: "Found observed difference in IAM role",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, testDiff, err := IsRoleUpToDate(tc.args.p, tc.args.role)
			if err != nil {
				t.Errorf("r: unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
			if tc.wantDiff == "" {
				if tc.wantDiff != testDiff {
					t.Errorf("r: -want, +got:\n%s", testDiff)
				}
			}

			if tc.wantDiff == "Found observed difference in IAM role" {
				if !strings.Contains(testDiff, tc.wantDiff) {
					t.Errorf("r: -want, +got:\n%s", testDiff)
				}
			}
		})
	}
}

func TestDiffIAMTags(t *testing.T) {
	type args struct {
		local  []v1beta1.Tag
		remote []iam.Tag
	}
	type want struct {
		add    []iam.Tag
		remove []string
	}
	cases := map[string]struct {
		args args
		want want
	}{
		"AllNew": {
			args: args{
				local: []v1beta1.Tag{
					{Key: "key", Value: "val"},
				},
			},
			want: want{
				add: []iam.Tag{
					{Key: aws.String("key"), Value: aws.String("val")},
				},
			},
		},
		"SomeNew": {
			args: args{
				local: []v1beta1.Tag{
					{Key: "key", Value: "val"},
					{Key: "key1", Value: "val1"},
					{Key: "key2", Value: "val2"},
				},
				remote: []iam.Tag{
					{Key: aws.String("key"), Value: aws.String("val")},
				},
			},
			want: want{
				add: []iam.Tag{
					{Key: aws.String("key1"), Value: aws.String("val1")},
					{Key: aws.String("key2"), Value: aws.String("val2")},
				},
			},
		},
		"Update": {
			args: args{
				local: []v1beta1.Tag{
					{Key: "key", Value: "different"},
					{Key: "key1", Value: "val1"},
					{Key: "key2", Value: "val2"},
				},
				remote: []iam.Tag{
					{Key: aws.String("key"), Value: aws.String("val")},
					{Key: aws.String("key1"), Value: aws.String("val1")},
					{Key: aws.String("key2"), Value: aws.String("val2")},
				},
			},
			want: want{
				add: []iam.Tag{
					{Key: aws.String("key"), Value: aws.String("different")},
				},
				remove: []string{"key"},
			},
		},
		"RemoveAll": {
			args: args{
				remote: []iam.Tag{
					{Key: aws.String("key"), Value: aws.String("val")},
					{Key: aws.String("key1"), Value: aws.String("val1")},
					{Key: aws.String("key2"), Value: aws.String("val2")},
				},
			},
			want: want{
				remove: []string{"key", "key1", "key2"},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tagCmp := cmpopts.SortSlices(func(i, j iam.Tag) bool {
				return aws.StringValue(i.Key) < aws.StringValue(j.Key)
			})
			add, remove := DiffIAMTags(tc.args.local, tc.args.remote)
			if diff := cmp.Diff(tc.want.add, add, tagCmp); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
			sort.Strings(tc.want.remove)
			sort.Strings(remove)
			if diff := cmp.Diff(tc.want.remove, remove, tagCmp); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
		})
	}
}
