/*
Copyright 2015 Gravitational, Inc.

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

// Package trail integrates trace errors with GRPC
package trail

import (
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/gravitational/trace"

	log "github.com/Sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

// Send is a high level function that:
// * logs error
// * converts error to GRPC error
// * attaches debug metadata to existing metadata if possible
// * sends the header to GRPC
func Send(ctx context.Context, err error) error {
	meta, ok := metadata.FromContext(ctx)
	if !ok {
		meta = metadata.New(nil)
	}
	if trace.IsDebug() {
		SetDebugInfo(err, meta)
	}
	log.Error(trace.DebugReport(err))
	if len(meta) != 0 {
		log.Infof("meta: %v", meta)
		err2 := grpc.SendHeader(ctx, meta)
		if err2 != nil {
			log.Errorf("failed to send metadata: %v", err2)
		}
	}
	return ToGRPC(err)
}

// DebugReportMetadata is a debug report metadata for the error
const DebugReportMetadata = "trace-debug-report"

// ToGRPC converts error to GRPC-compatible error
func ToGRPC(err error) error {
	userMessage := trace.UserMessage(err)
	if trace.IsNotFound(err) {
		return grpc.Errorf(codes.NotFound, userMessage)
	}
	if trace.IsAlreadyExists(err) {
		return grpc.Errorf(codes.AlreadyExists, userMessage)
	}
	if trace.IsAccessDenied(err) {
		return grpc.Errorf(codes.PermissionDenied, userMessage)
	}
	if trace.IsCompareFailed(err) {
		return grpc.Errorf(codes.FailedPrecondition, userMessage)
	}
	if trace.IsBadParameter(err) || trace.IsOAuth2(err) {
		return grpc.Errorf(codes.InvalidArgument, userMessage)
	}
	if trace.IsLimitExceeded(err) {
		return grpc.Errorf(codes.ResourceExhausted, userMessage)
	}
	if trace.IsConnectionProblem(err) {
		return grpc.Errorf(codes.Unavailable, userMessage)
	}
	return grpc.Errorf(codes.Unknown, userMessage)
}

// FromGRPC converts error from GRPC error back to trace.Error
// Optional debug information can be recovered using metadata
func FromGRPC(err error, args ...interface{}) error {
	code := grpc.Code(err)
	message := grpc.ErrorDesc(err)
	var e error
	switch code {
	case codes.OK:
		return nil
	case codes.NotFound:
		e = &trace.NotFoundError{Message: message}
	case codes.AlreadyExists:
		e = &trace.AlreadyExistsError{Message: message}
	case codes.PermissionDenied:
		e = &trace.AccessDeniedError{Message: message}
	case codes.FailedPrecondition:
		e = &trace.CompareFailedError{Message: message}
	case codes.InvalidArgument:
		e = &trace.BadParameterError{Message: message}
	case codes.ResourceExhausted:
		e = &trace.LimitExceededError{Message: message}
	case codes.Unavailable:
		e = &trace.ConnectionProblemError{Message: message}
	default:
		e = errors.New(message)
	}
	if len(args) != 0 {
		if meta, ok := args[0].(metadata.MD); ok {
			e = DecodeDebugInfo(e, meta)
		}
	}
	return e
}

// SetDebugInfo adds debug metadata about error to request
func SetDebugInfo(err error, meta metadata.MD) {
	if _, ok := err.(*trace.TraceErr); !ok {
		return
	}
	out, err := json.Marshal(err)
	if err != nil {
		return
	}
	meta[DebugReportMetadata] = []string{
		base64.StdEncoding.EncodeToString(out),
	}
}

// DecodeDebugInfo decodes debug information about error
// from the metadata and returns error with enriched metadata about it
func DecodeDebugInfo(err error, meta metadata.MD) error {
	if meta == nil {
		return err
	}
	encoded, ok := meta[DebugReportMetadata]
	if !ok || len(encoded) != 1 {
		return err
	}
	data, err2 := base64.StdEncoding.DecodeString(encoded[0])
	if err2 != nil {
		return err
	}
	var raw trace.RawTrace
	if err2 := json.Unmarshal(data, &raw); err2 != nil {
		return err
	}
	if len(raw.Traces) != 0 && len(raw.Err) != 0 {
		return &trace.TraceErr{Traces: raw.Traces, Err: err, Message: raw.Message}
	}
	return err
}