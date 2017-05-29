// Licensed to Pulumi Corporation ("Pulumi") under one or more
// contributor license agreements.  See the NOTICE file distributed with
// this work for additional information regarding copyright ownership.
// Pulumi licenses this file to You under the Apache License, Version 2.0
// (the "License"); you may not use this file except in compliance with
// the License.  You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package s3

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"reflect"

	"github.com/aws/aws-sdk-go/aws"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
	"github.com/pulumi/lumi/pkg/resource"
	"github.com/pulumi/lumi/pkg/util/mapper"
	"github.com/pulumi/lumi/sdk/go/pkg/lumirpc"
	"golang.org/x/net/context"

	"github.com/pulumi/lumi/lib/aws/provider/awsctx"
	"github.com/pulumi/lumi/lib/aws/rpc/s3"
)

const BucketToken = s3.BucketToken

// constants for the various bucket limits.
const (
	minBucketName = 3
	maxBucketName = 63 // TODO: consider supporting legacy us-east-1 (255) limits.
)

// NewBucketProvider creates a provider that handles S3 bucket operations.
func NewBucketProvider(ctx *awsctx.Context) lumirpc.ResourceProviderServer {
	ops := &buckProvider{ctx}
	return s3.NewBucketProvider(ops)
}

type buckProvider struct {
	ctx *awsctx.Context
}

// Check validates that the given property bag is valid for a resource of the given type.
func (p *buckProvider) Check(ctx context.Context, obj *s3.Bucket) ([]mapper.FieldError, error) {
	var failures []mapper.FieldError
	if name := obj.BucketName; name != nil {
		if len(*name) < minBucketName {
			failures = append(failures,
				mapper.NewFieldErr(reflect.TypeOf(obj), s3.Bucket_BucketName,
					fmt.Errorf("less than minimum length of %v", minBucketName)))
		} else if len(*name) > maxBucketName {
			failures = append(failures,
				mapper.NewFieldErr(reflect.TypeOf(obj), s3.Bucket_BucketName,
					fmt.Errorf("exceeded maximum length of %v", maxBucketName)))
		}
	}
	// TODO: by default, only up to 100 buckets in an account.
	// TODO: check the vailidity of names (see http://docs.aws.amazon.com/AmazonS3/latest/dev/BucketRestrictions.html).
	// TODO: check the validity of the access control field.
	return failures, nil
}

// Create allocates a new instance of the provided resource and returns its unique ID afterwards.  (The input ID
// must be blank.)  If this call fails, the resource must not have been created (i.e., it is "transacational").
func (p *buckProvider) Create(ctx context.Context, obj *s3.Bucket) (resource.ID, error) {
	// If an explicit name is given, use it.  Otherwise, auto-generate a name in part based on the resource name.
	// TODO: use the URN, not just the name, to enhance global uniqueness.
	// TODO: even for explicit names, we should consider mangling it somehow, to reduce multi-instancing conflicts.
	var id resource.ID
	if obj.BucketName != nil {
		id = resource.ID(*obj.BucketName)
	} else {
		id = resource.NewUniqueHexID(obj.Name+"-", maxBucketName, sha1.Size)
	}
	var acl *string
	if obj.AccessControl != nil {
		acl = aws.String(string(*obj.AccessControl))
	}
	fmt.Printf("Creating S3 Bucket '%v' with name '%v'\n", obj.Name, id)
	create := &awss3.CreateBucketInput{
		Bucket: id.StringPtr(),
		ACL:    acl,
	}

	// Now go ahead and perform the action.
	if _, err := p.ctx.S3().CreateBucket(create); err != nil {
		return "", err
	}

	// Wait for the bucket to be ready and then return the ID (just its name).
	fmt.Printf("S3 Bucket created: %v; waiting for it to become active\n", id)
	if err := p.waitForBucketState(id, true); err != nil {
		return "", err
	}
	return id, nil
}

// Get reads the instance state identified by ID, returning a populated resource object, or an error if not found.
func (p *buckProvider) Get(ctx context.Context, id resource.ID) (*s3.Bucket, error) {
	return nil, errors.New("Not yet implemented")
}

// InspectChange checks what impacts a hypothetical update will have on the resource's properties.
func (p *buckProvider) InspectChange(ctx context.Context, id resource.ID,
	old *s3.Bucket, new *s3.Bucket, diff *resource.ObjectDiff) ([]string, error) {
	return nil, errors.New("Not yet implemented")
}

// Update updates an existing resource with new values.  Only those values in the provided property bag are updated
// to new values.  The resource ID is returned and may be different if the resource had to be recreated.
func (p *buckProvider) Update(ctx context.Context, id resource.ID,
	old *s3.Bucket, new *s3.Bucket, diff *resource.ObjectDiff) error {
	return errors.New("Not yet implemented")
}

// Delete tears down an existing resource with the given ID.  If it fails, the resource is assumed to still exist.
func (p *buckProvider) Delete(ctx context.Context, id resource.ID) error {
	// First, perform the deletion.
	fmt.Printf("Deleting S3 Bucket '%v'\n", id)
	if _, err := p.ctx.S3().DeleteBucket(&awss3.DeleteBucketInput{
		Bucket: id.StringPtr(),
	}); err != nil {
		return err
	}

	// Wait for the bucket to actually become deleted before returning.
	fmt.Printf("S3 Bucket delete request submitted; waiting for it to delete\n")
	return p.waitForBucketState(id, false)
}

func (p *buckProvider) waitForBucketState(id resource.ID, exist bool) error {
	succ, err := awsctx.RetryUntil(
		p.ctx,
		func() (bool, error) {
			if _, err := p.ctx.S3().HeadBucket(&awss3.HeadBucketInput{
				Bucket: id.StringPtr(),
			}); err != nil {
				if awsctx.IsAWSError(err, "NotFound", "NoSuchBucket") {
					// The bucket is missing; if exist==false, we're good, otherwise keep retrying.
					return !exist, nil
				}
				return false, err // anything other than "bucket missing" is a real error; propagate it.
			}

			// If we got here, the bucket was found; if exist==true, we're good; else, keep retrying.
			return exist, nil
		},
	)
	if err != nil {
		return err
	} else if !succ {
		var reason string
		if exist {
			reason = "created"
		} else {
			reason = "deleted"
		}
		return fmt.Errorf("S3 bucket '%v' did not become %v", id, reason)
	}
	return nil
}
