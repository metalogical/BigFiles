package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/go-chi/chi"
	"github.com/metalogical/BigFiles/batch"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/s3utils"
)

type Options struct {
	// required
	Endpoint string
	NoSSL    bool
	Bucket   string

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

		switch req.Operation {
		case "download":
			info, err := s.client.StatObject(r.Context(), s.bucket, s.key(in.OID), minio.StatObjectOptions{})
			if err != nil {
				out.Error = &batch.ObjectError{
					Code:    404,
					Message: err.Error(),
				}
				continue
			}

			href, err := s.client.PresignedGetObject(r.Context(), s.bucket, s.key(in.OID), s.ttl, nil)
			if err != nil {
				out.Error = &batch.ObjectError{
					Code:    404,
					Message: err.Error(),
				}
				continue
			}

			out.Size = int(info.Size)
			out.Actions = &batch.Actions{
				Download: &batch.Action{
					HRef:      href.String(),
					ExpiresIn: int(s.ttl / time.Second),
				},
			}

		case "upload":
			_, err := s.client.StatObject(r.Context(), s.bucket, s.key(in.OID), minio.StatObjectOptions{})
			if err == nil {
				// already exists, omit actions
				continue
			}

			href, err := s.client.PresignedPutObject(r.Context(), s.bucket, s.key(in.OID), s.ttl)
			if err != nil {
				out.Error = &batch.ObjectError{
					Code:    404,
					Message: err.Error(),
				}
				continue
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
