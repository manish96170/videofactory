package cloudprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

func (j *Job) transfer(ctx context.Context, credential Credential, ref JobRef, artifact Artifact, sink Sink) (result DownloadResult, err error) {
	stream, err := j.adapter.transport.Download(ctx, credential, ref, artifact)
	// Even a stream accompanying failure is an owned resource to close. Never
	// read it or hand accompanying data to the sink.
	if !nilLike(stream) {
		defer func() {
			if stream != nil {
				if closeErr := stream.Close(); closeErr != nil && err == nil {
					err = ErrDependency
					result = DownloadResult{}
				}
			}
		}()
	}
	if e := ctx.Err(); e != nil {
		return result, errors.Join(e, err)
	}
	if err != nil {
		return result, err
	}
	if nilLike(stream) {
		return result, ErrResult
	}
	target := Target{RequestID: ref.RequestID, ShotID: j.shot, Artifact: artifact}
	stage, beginErr := sink.Begin(ctx, target)
	committed := false
	if !nilLike(stage) {
		defer func() {
			if committed {
				return
			}
			cleanupCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), j.adapter.policy.CleanupTimeout)
			defer stop()
			cleanupErr := stage.Abort(cleanupCtx)
			if cleanupErr != nil || cleanupCtx.Err() != nil {
				err = errors.Join(ErrCleanup, err)
			}
			result = DownloadResult{}
		}()
	}
	if e := ctx.Err(); e != nil {
		return result, e
	}
	if beginErr != nil || nilLike(stage) {
		return result, ErrSink
	}
	hash := sha256.New()
	buf := make([]byte, 32*1024)
	var size int64
	emptyReads := 0
	for {
		if e := ctx.Err(); e != nil {
			return result, e
		}
		// Read at most the expected remainder plus one overflow-detection byte.
		n, readErr := stream.Read(buf[:min(int64(len(buf)), artifact.Size-size+1)])
		if e := ctx.Err(); e != nil {
			return result, errors.Join(e, readErr)
		}
		if n < 0 || n > len(buf) || int64(n) > artifact.Size-size {
			return result, ErrIntegrity
		}
		if n > 0 {
			emptyReads = 0
			written, writeErr := stage.Write(buf[:n])
			if e := ctx.Err(); e != nil {
				return result, e
			}
			if writeErr != nil || written != n {
				return result, ErrSink
			}
			_, _ = hash.Write(buf[:n])
			size += int64(n)
		} else {
			emptyReads++
			if emptyReads > 100 {
				return result, ErrDependency
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, readErr
		}
	}
	if size != artifact.Size || hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return result, ErrIntegrity
	}
	closeErr := stream.Close()
	stream = nil
	if e := ctx.Err(); e != nil {
		return result, e
	}
	if closeErr != nil {
		return result, ErrDependency
	}
	if err := stage.Commit(ctx); err != nil {
		return result, ErrSink
	}
	if e := ctx.Err(); e != nil {
		return result, e
	}
	committed = true
	return DownloadResult{Target: target, Quality: j.quality, Provenance: j.adapter.Provenance()}, nil
}
