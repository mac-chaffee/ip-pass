package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	traefikv1alpha1 "github.com/traefik/traefik/v2/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

//go:embed static/*
var embeded embed.FS

func init() {
	log.SetLogger(zap.New())
}

// Config is the overall config for ip-pass
type Config struct {
	middlewareName      string
	middlewareNamespace string
	timeout             time.Duration
	bindAddr            string
	// The depth in the X-Forwarded-For header to pull the real IP from.
	// This is a 1-indexed reverse index which works just like https://doc.traefik.io/traefik/middlewares/http/ipallowlist/#ipstrategydepth
	xffDepth  int
	ignoreXff bool
}

func NewConfigFromFlags() *Config {
	config := &Config{}

	flag.StringVar(&config.middlewareName, "middleware-name", "ip-pass-allowlist", "Name of the Middleware.")
	flag.StringVar(&config.middlewareNamespace, "middleware-namespace", "default", "Namespace of the Middleware.")
	flag.DurationVar(&config.timeout, "timeout", 10*time.Second, "Timeout duration for k8s API requests.")
	flag.StringVar(&config.bindAddr, "bind-addr", ":8080", "Address to bind the HTTP server.")
	flag.IntVar(&config.xffDepth, "xff-depth", 0, "Depth in X-Forwarded-For header to pull real IP from. Set to zero to ignore XFF and just use the observed client IP.")

	flag.Parse()

	return config
}

type Server struct {
	client client.Client
	config *Config
	log    logr.Logger
}

// getClientCIDR parses X-Forwarded-For then returns a masked CIDR.
// Ex: "203.0.113.111, 203.0.113.222", "1" -> "203.0.113.0/24"
// Ex: "2001:0db8::123" -> "2001:db8::/64"
func getClientCIDR(xForwardedFor string, depth int) (string, error) {
	// When depth=0, we just check the single observed client IP
	depth = max(depth, 1)
	xff := strings.Split(xForwardedFor, ",")
	if len(xff) < depth {
		return "", fmt.Errorf("X-Forwarded-For header was empty or too short")
	}
	clientIP := strings.TrimSpace(xff[len(xff)-depth])
	clientAddr, err := netip.ParseAddr(clientIP)
	if err != nil {
		return "", err
	}

	// Round up to the nearest (normal) subnet in case of CGNAT for IPv4
	// or privacy extensions in IPv6
	if clientAddr.Is4() {
		return netip.PrefixFrom(clientAddr, 24).Masked().String(), nil
	}
	return netip.PrefixFrom(clientAddr, 64).Masked().String(), nil
}

// patchMiddleware has to fetch the current middleware in order to
// append the CIDR to the IPAllowList instead of overwriting it.
// This is due to no support for strategic merge patches for CRDs.
// We call this function from inside a RetryOnConflict block
// to avoid race conditions.
func (s *Server) patchMiddleware(ctx context.Context, clientCIDR string) (bool, error) {
	// Get the latest version
	existing := &traefikv1alpha1.Middleware{}
	if err := s.client.Get(ctx, types.NamespacedName{
		Name:      s.config.middlewareName,
		Namespace: s.config.middlewareNamespace,
	}, existing); err != nil {
		return false, err
	}
	var existingIPs []string
	// Could be stored in either IPWhiteList or IPAllowList depending on the version
	if existing.Spec.IPWhiteList != nil {
		existingIPs = existing.Spec.IPWhiteList.SourceRange
	} else if existing.Spec.IPAllowList != nil {
		existingIPs = existing.Spec.IPAllowList.SourceRange
	} else {
		existingIPs = []string{}
	}

	// Naive looping is faster for small lists
	for _, ip := range existingIPs {
		if ip == clientCIDR {
			s.log.Info("Client CIDR was already allow-listed", "cidr", clientCIDR)
			return false, nil
		}
	}
	newIPs := append(existingIPs, clientCIDR)

	patch := &traefikv1alpha1.Middleware{
		ObjectMeta: metav1.ObjectMeta{
			Name:            s.config.middlewareName,
			Namespace:       s.config.middlewareNamespace,
			ResourceVersion: existing.ResourceVersion,
		},
		Spec: traefikv1alpha1.MiddlewareSpec{
			IPWhiteList: &dynamic.IPWhiteList{
				SourceRange: newIPs,
			},
		},
	}
	// Make sure to save the IPs to the same-named value
	if existing.Spec.IPWhiteList != nil {
		patch.Spec = traefikv1alpha1.MiddlewareSpec{
			IPWhiteList: &dynamic.IPWhiteList{
				SourceRange: newIPs,
			},
		}
	} else if existing.Spec.IPAllowList != nil {
		patch.Spec = traefikv1alpha1.MiddlewareSpec{
			IPAllowList: &dynamic.IPAllowList{
				SourceRange: newIPs,
			},
		}
	} else {
		// This probably shouldn't happen, but this could be a bug if run on older traefik versions
		patch.Spec = traefikv1alpha1.MiddlewareSpec{
			IPAllowList: &dynamic.IPAllowList{
				SourceRange: newIPs,
			},
		}
	}
	err := s.client.Patch(ctx, patch, client.Merge)
	if err != nil {
		return false, err
	}
	s.log.Info("Added CIDR to middleware", "cidr", clientCIDR)
	return true, nil

}

func (s *Server) addIPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ips := r.Header.Get("X-Forwarded-For")
	if s.config.xffDepth == 0 {
		// xff disabled, so just pull IP from the observed client IP.
		ips, _, _ = net.SplitHostPort(r.RemoteAddr)
	}
	clientCIDR, err := getClientCIDR(ips, s.config.xffDepth)
	if err != nil {
		s.log.Error(err, "Failed to parse client IP address", "IPs", ips)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var created bool
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		created, err = s.patchMiddleware(r.Context(), clientCIDR)
		return err
	})

	if err != nil {
		s.log.Error(err, "Failed to patch middleware", "cidr", clientCIDR)
		http.Error(w, fmt.Sprintf("Failed to patch middleware: %v", err), http.StatusInternalServerError)
		return
	}
	// Support returning a good status code for API requests (hacky, sorry)
	if strings.HasPrefix(r.RequestURI, "/api/") {
		if created {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	} else {
		w.Header().Set("Location", "/success")
		w.WriteHeader(http.StatusSeeOther)
	}
	// TODO: Support GET requests and arbitrary redirects? Arbitrary link in response?
}

// createMiddlewareIfMissing ensures that the configured Middleware exists in the cluster
// so that we don't have to re-check if the middle ware exists on every request.
func createMiddlewareIfMissing(ctx context.Context, c client.Client, config *Config) (bool, error) {
	existing := &traefikv1alpha1.Middleware{}
	err := c.Get(ctx, types.NamespacedName{
		Name:      config.middlewareName,
		Namespace: config.middlewareNamespace,
	}, existing)

	if err == nil {
		return false, nil
	}
	switch typ := err.(type) {
	default:
		return false, err
	case apierrors.APIStatus:
		if typ.Status().Code != http.StatusNotFound {
			return false, err
		}
	}
	// If we got here, the Middleware doesn't exist, so create it
	basicAuth := &traefikv1alpha1.Middleware{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.middlewareName,
			Namespace: config.middlewareNamespace,
		},
		Spec: traefikv1alpha1.MiddlewareSpec{
			// k3s still uses the old name
			IPWhiteList: &dynamic.IPWhiteList{
				SourceRange: []string{},
			},
		},
	}
	if err := c.Create(ctx, basicAuth); err != nil {
		return false, fmt.Errorf("Failed to create middleware: %v", err)
	}
	return true, nil
}

// addHeaders adds security and caching headers for serving static content
func addHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "script-src 'self'")
	w.Header().Set("Cache-Control", "max-age=1800")
}

func main() {
	logger := log.Log.WithName("entrypoint")
	appConfig := NewConfigFromFlags()

	// Create scheme and add Traefik types
	scheme := runtime.NewScheme()
	_ = traefikv1alpha1.AddToScheme(scheme)

	cfg, err := config.GetConfig()
	if err != nil {
		panic(err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), appConfig.timeout)
	defer cancel()
	created, err := createMiddlewareIfMissing(ctx, c, appConfig)
	if err != nil {
		panic(err)
	}
	if created {
		logger.Info("Created Middleware", "name", appConfig.middlewareName, "namespace", appConfig.middlewareNamespace)
	} else {
		logger.Info("Verified Middleware exists", "name", appConfig.middlewareName, "namespace", appConfig.middlewareNamespace)
	}

	server := &Server{
		client: c,
		config: appConfig,
		log:    log.Log.WithName("server"),
	}

	indexBytes, err := embeded.ReadFile("static/index.html")
	if err != nil {
		panic(err)
	}
	indexContent := string(indexBytes)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Fix Golang's non-compliant behavior of always returning the index instead of 404s
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		addHeaders(w)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(indexContent))
	})

	successBytes, err := embeded.ReadFile("static/success.html")
	if err != nil {
		panic(err)
	}
	successContent := string(successBytes)

	http.HandleFunc("/success", func(w http.ResponseWriter, r *http.Request) {
		addHeaders(w)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(successContent))
	})
	http.HandleFunc("/add-ip", server.addIPHandler)
	http.HandleFunc("/api/add-ip", server.addIPHandler)
	http.HandleFunc("/healthz", func(http.ResponseWriter, *http.Request) {})

	logger.Info("Starting server", "addr", appConfig.bindAddr)
	err = http.ListenAndServe(appConfig.bindAddr, nil)
	if err != nil {
		panic(err)
	}
}
