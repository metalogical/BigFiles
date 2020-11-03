package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/go-chi/chi"
	"github.com/metalogical/BigFiles/batch"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/s3utils"
)

var S3PutLimit int = 5*int(math.Pow10(9)) - 1 // 5GB - 1
var oidRegexp = regexp.MustCompile("^[a-f0-9]{64}$")

type Options struct {
	// required
	Endpoint     string
	NoSSL        bool
	Bucket       string
	S3Accelerate bool

	// minio auth (required)
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string

	// optional
	TTL    time.Duration // defaults to 1 hour
	Prefix string

	IsAuthorized func(string, string) error
}

func (o Options) imputeFromEnv() (Options, error) {
	if o.Endpoint == "" {
		region := os.Getenv("AWS_REGION")
		if region == "" {
			return o, errors.New("endpoint required")
		}
		o.Endpoint = fmt.Sprintf("s3.%s.amazonaws.com", region)
	}
	if o.AccessKeyID == "" {
		if s3utils.IsAmazonEndpoint(url.URL{Host: o.Endpoint}) {
			o.AccessKeyID = os.Getenv("AWS_ACCESS_KEY_ID")
			if o.AccessKeyID == "" {
				return o, fmt.Errorf("AWS access key ID required for %s", o.Endpoint)
			}
			o.SecretAccessKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
			if o.SecretAccessKey == "" {
				return o, fmt.Errorf("AWS secret access key required for %s", o.Endpoint)
			}
			o.SessionToken = os.Getenv("AWS_SESSION_TOKEN")
		} else {
			return o, fmt.Errorf("access key & id required for %s", o.Endpoint)
		}
	}
	if o.Bucket == "" {
		return o, fmt.Errorf("bucket required")
	}
	if o.TTL == 0 {
		o.TTL = time.Hour
	}

	return o, nil
}

func New(o Options) (http.Handler, error) {
	o, err := o.imputeFromEnv()
	if err != nil {
		return nil, err
	}

	// Initialize minio client object.
	client, err := minio.New(o.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(o.AccessKeyID, o.SecretAccessKey, o.SessionToken),
		Secure: !o.NoSSL,
	})
	if err != nil {
		return nil, err
	}
	if o.S3Accelerate {
		client.SetS3TransferAccelerate("s3-accelerate.amazonaws.com")
	}

	s := &server{
		client:       client,
		bucket:       o.Bucket,
		prefix:       o.Prefix,
		ttl:          o.TTL,
		isAuthorized: o.IsAuthorized,
	}

	r := chi.NewRouter()
	r.Post("/objects/batch", s.handleBatch)

	return r, nil
}

type server struct {
	client *minio.Client
	bucket string
	ttl    time.Duration
	prefix string

	isAuthorized func(string, string) error
}

func (s *server) key(oid string) string {
	return s.prefix + oid
}

func (s *server) handleBatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if s.isAuthorized != nil {
		var err error
		if username, password, ok := r.BasicAuth(); ok {
			err = s.isAuthorized(username, password)
			if err != nil {
				err = fmt.Errorf("Unauthorized: %w", err)
			}
		} else {
			err = errors.New("Unauthorized")
		}

		if err != nil {
			w.Header().Set("LFS-Authenticate", `Basic realm="Git LFS"`)
			w.WriteHeader(401)
			must(json.NewEncoder(w).Encode(batch.ErrorResponse{
				Message: err.Error(),
			}))
			return
		}
	}

	var req batch.Request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		w.WriteHeader(404)
		must(json.NewEncoder(w).Encode(batch.ErrorResponse{
			Message: "could not parse request",
			DocURL:  "https://github.com/git-lfs/git-lfs/blob/v2.12.0/docs/api/batch.md#requests",
		}))
		return
	}

	var resp batch.Response

	for _, in := range req.Objects {
		resp.Objects = append(resp.Objects, batch.Object{
			OID:  in.OID,
			Size: in.Size,
		})
		out := &resp.Objects[len(resp.Objects)-1]

		if !oidRegexp.MatchString(in.OID) {
			out.Error = &batch.ObjectError{
				Code:    422,
				Message: "oid must be a SHA-256 hash in lower case hexadecimal",
			}
			continue
		}

		switch req.Operation {
		case "download":
			if info, err := s.client.StatObject(r.Context(), s.bucket, s.key(in.OID), minio.StatObjectOptions{}); err != nil {
				out.Error = &batch.ObjectError{
					Code:    404,
					Message: err.Error(),
				}
				continue
			} else if in.Size != int(info.Size) {
				out.Error = &batch.ObjectError{
					Code:    422,
					Message: "found object with wrong size",
				}
			}

			href, err := s.client.PresignedGetObject(r.Context(), s.bucket, s.key(in.OID), s.ttl, nil)
			if err != nil {
				panic(err)
			}

			out.Actions = &batch.Actions{
				Download: &batch.Action{
					HRef:      href.String(),
					ExpiresIn: int(s.ttl / time.Second),
				},
			}

		case "upload":
			if info, err := s.client.StatObject(r.Context(), s.bucket, s.key(in.OID), minio.StatObjectOptions{}); err == nil {
				if in.Size != int(info.Size) {
					out.Error = &batch.ObjectError{
						Code:    422,
						Message: "existing object with wrong size",
					}
				}
				// already exists, omit actions
				continue
			}

			if out.Size > S3PutLimit {
				out.Error = &batch.ObjectError{
					Code:    422,
					Message: "cannot upload objects larger than 5GB to S3 via LFS basic transfer adapter",
				}
				continue
			}

			href, err := s.client.PresignedPutObject(r.Context(), s.bucket, s.key(in.OID), s.ttl)
			if err != nil {
				panic(err)
			}

			out.Actions = &batch.Actions{
				Upload: &batch.Action{
					HRef:      href.String(),
					ExpiresIn: int(s.ttl / time.Second),
				},
			}
		}
	}

	must(json.NewEncoder(w).Encode(resp))
}

// --

func must(err error) {
	if err != nil {
		panic(err)
	}
}
