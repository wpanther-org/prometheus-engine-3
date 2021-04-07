package operator

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/google/gpe-collector/pkg/operator/apis/monitoring/v1alpha1"
	"github.com/pkg/errors"
	"k8s.io/client-go/kubernetes/scheme"

	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type admitFn func(*v1.AdmissionReview) (*v1.AdmissionResponse, error)

// AdmissionServer serves Kubernetes resource admission requests.
type AdmissionServer struct {
	logger  log.Logger
	decoder runtime.Decoder
}

// NewAdmissionServer returns a new AdmissionServer with the provided logger.
func NewAdmissionServer(logger log.Logger) *AdmissionServer {
	return &AdmissionServer{
		logger:  logger,
		decoder: scheme.Codecs.UniversalDeserializer(),
	}
}

// serveAdmission returns a http handler closure that evaluates Kubernetes admission
// requests. Encountered errors are logged and potentially returned in the admission
// response.
func (a *AdmissionServer) serveAdmission(admit admitFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		level.Debug(a.logger).Log(
			"msg", "webhook called",
			"method", r.Method,
			"host", r.Host,
			"path", r.URL.Path)

		var req, resp v1.AdmissionReview
		// Read, decode, and evaluate admission request.
		if data, err := ioutil.ReadAll(r.Body); err != nil {
			level.Error(a.logger).Log("msg", "reading request body", "err", err)
			resp.Response = toAdmissionResponse(err)
		} else if _, _, err := a.decoder.Decode(data, nil, &req); err != nil {
			level.Error(a.logger).Log("msg", "decoding request body", "err", err)
			resp.Response = toAdmissionResponse(err)
		} else if ar, err := admit(&req); err != nil {
			level.Error(a.logger).Log("msg", "admitting admission request", "err", err)
			resp.Response = toAdmissionResponse(err)
		} else {
			resp.Response = ar
		}
		// Return the same API, Kind, and UID as long as incoming
		// request data was decoded properly.
		if req.Request != nil {
			resp.APIVersion = req.APIVersion
			resp.Kind = req.Kind
			resp.Response.UID = req.Request.UID
		}

		// Write the admission response.
		if respBytes, err := json.Marshal(resp); err != nil {
			level.Error(a.logger).Log("msg", "encoding response body", "err", err)
		} else if _, err := w.Write(respBytes); err != nil {
			level.Error(a.logger).Log("msg", "writing response body", "err", err)
		}
	}
}

// admitPodMonitoring evaluates incoming PodMonitoring resources to ensure
// they are a valid resource.
func admitPodMonitoring(ar *v1.AdmissionReview) (*v1.AdmissionResponse, error) {
	var pm = &v1alpha1.PodMonitoring{}
	// Ensure the requested resource is a PodMonitoring.
	if ar.Request.Resource != v1alpha1.PodMonitoringResource() {
		return nil, fmt.Errorf("expected resource to be %v, but received %v", v1alpha1.PodMonitoringResource(), ar.Request.Resource)
		// Unmarshall payload to PodMonitoring stuct.
	} else if err := json.Unmarshal(ar.Request.Object.Raw, pm); err != nil {
		return nil, errors.Wrap(err, "unmarshalling admission request to podmonitoring")
		// Check valid relabel mappings.
	} else if _, err := labelMappingRelabelConfigs(pm.Spec.TargetLabels.FromPod, podLabelPrefix); err != nil {
		return nil, errors.Wrap(err, "checking label mappings")
	}

	return &v1.AdmissionResponse{Allowed: true}, nil
}

// admitServiceMonitoring evaluates incoming ServiceMonitoring resources to ensure
// they are a valid resource.
func admitServiceMonitoring(ar *v1.AdmissionReview) (*v1.AdmissionResponse, error) {
	var sm = &v1alpha1.ServiceMonitoring{}
	// Ensure the requested resource is a ServiceMonitoring.
	if ar.Request.Resource != v1alpha1.ServiceMonitoringResource() {
		return nil, fmt.Errorf("expected resource to be %v, but received %v", v1alpha1.ServiceMonitoringResource(), ar.Request.Resource)
		// Unmarshall payload to ServiceMonitoring stuct.
	} else if err := json.Unmarshal(ar.Request.Object.Raw, sm); err != nil {
		return nil, errors.Wrap(err, "unmarshalling admission request to servicemonitoring")
		// Check valid relabel mappings.
	} else if _, err := labelMappingRelabelConfigs(sm.Spec.TargetLabels.FromPod, podLabelPrefix); err != nil {
		return nil, errors.Wrap(err, "checking pod label mappings")
	} else if _, err := labelMappingRelabelConfigs(sm.Spec.TargetLabels.FromService, serviceLabelPrefix); err != nil {
		return nil, errors.Wrap(err, "checking service label mappings")
	}

	return &v1.AdmissionResponse{Allowed: true}, nil
}

// toAdmissionResponse is a helper function that returns an AdmissionResponse
// containing a message of the provided error.
func toAdmissionResponse(err error) *v1.AdmissionResponse {
	return &v1.AdmissionResponse{
		Allowed: false, // make explicit for clarity
		Result: &metav1.Status{
			Status:  metav1.StatusFailure,
			Message: err.Error(),
		},
	}
}
