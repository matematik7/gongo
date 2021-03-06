package s3storage

import (
	"io"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/pkg/errors"
)

type S3Storage struct {
	uploader *s3manager.Uploader
	client   *s3.S3

	bucket     string
	signedUrls bool
}

func New(awsSession *session.Session, bucket string, signedUrls bool) (*S3Storage, error) {
	s := &S3Storage{
		bucket:     bucket,
		uploader:   s3manager.NewUploader(awsSession),
		client:     s3.New(awsSession),
		signedUrls: signedUrls,
	}

	return s, nil
}

func (s *S3Storage) URL(name string) (string, error) {
	req, _ := s.client.GetObjectRequest(&s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &name,
	})
	if s.signedUrls {
		url, err := req.Presign(time.Hour)
		if err != nil {
			return "", errors.Wrap(err, "could not presign url")
		}
		return url, nil
	}

	err := req.Build()
	if err != nil {
		return "", errors.Wrap(err, "could not build url")
	}

	return req.HTTPRequest.URL.String(), nil
}

func (s *S3Storage) Save(name string, input io.Reader) error {
	// TODO: set content type and download filename
	_, err := s.uploader.Upload(&s3manager.UploadInput{
		Bucket: &s.bucket,
		Key:    &name,
		Body:   input,
	})
	if err != nil {
		return errors.Wrapf(err, "could not upload file %s", name)
	}

	return nil
}

func (s *S3Storage) Delete(name string) error {
	_, err := s.client.DeleteObject(&s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &name,
	})
	if err != nil {
		return errors.Wrapf(err, "could not delete file %s", name)
	}

	return nil
}

func (s *S3Storage) List(prefix string) ([]string, error) {
	results := []string{}

	err := s.client.ListObjectsV2Pages(&s3.ListObjectsV2Input{
		Bucket:    &s.bucket,
		Delimiter: aws.String("/"),
		Prefix:    &prefix,
	}, func(output *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, cp := range output.CommonPrefixes {
			if *cp.Prefix != prefix {
				results = append(results, *cp.Prefix)
			}
		}
		for _, object := range output.Contents {
			if *object.Key != prefix {
				results = append(results, *object.Key)
			}
		}
		return true
	})
	if err != nil {
		return nil, errors.Wrap(err, "could not list objects")
	}

	return results, nil
}
