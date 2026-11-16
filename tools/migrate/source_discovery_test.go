package migrate

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/mikd1/tag-files/app/internal/objectstore"
)

func TestSourceBucketDiscoveryIncludesExtraBucketsInStableOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/" {
			t.Errorf("unexpected discovery request: %s %s", r.Method, r.URL)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Buckets><Bucket><Name>thumbnail</Name></Bucket><Bucket><Name>extra-backup</Name></Bucket><Bucket><Name>library</Name></Bucket></Buckets></ListAllMyBucketsResult>`)
	}))
	defer server.Close()
	buckets, err := DiscoverSourceBuckets(ctx, objectstore.Config{Endpoint: server.URL, Region: "us-east-1", AccessKey: "access", SecretKey: "secret", HTTPClient: server.Client()})
	if err != nil || !reflect.DeepEqual(buckets, []string{"extra-backup", "library", "thumbnail"}) {
		t.Fatalf("discovery lost buckets: %v %v", buckets, err)
	}
}
