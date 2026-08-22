package basemap

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// JobConfig is the standing, deployment-wide configuration an update Job
// runs with — the operator-supplied half, as opposed to a request's own
// bbox/maxzoom/buildDate. Mirrors domestique-chart's basemapUpdate values
// block exactly; main.go builds this from the same config file.
type JobConfig struct {
	// TilesNamespace is where the Job runs and where the tiles pod lives.
	TilesNamespace string
	// TilesPodSelector finds the running tiles pod to copy into — a label
	// selector string, e.g. "app.kubernetes.io/name=tiles".
	TilesPodSelector string
	// CopyServiceAccount is the second, narrowly-scoped identity
	// (domestique-chart's own basemap-rbac.yaml) the copy container runs
	// as — never this app's own, so pods/exec is never something the
	// always-on app pod itself can do.
	CopyServiceAccount    string
	ExtractImage          string // repository:tag
	CopyImage             string // repository:tag
	CPURequest            string
	MemRequest            string
	MemLimit              string
	WorkVolumeSize        string
	ActiveDeadlineSeconds int64
}

// Client wraps a Kubernetes clientset with the config an update Job needs,
// so callers never touch client-go types directly.
type Client struct {
	clientset kubernetes.Interface
	cfg       JobConfig
}

// InCluster builds a Client from the pod's own mounted ServiceAccount —
// nil, nil when not running in a cluster (a laptop, most tests), the same
// "quietly unavailable" shape Komoot/Garmin/Wahoo already use for a
// deployment-wide credential that simply isn't there.
func InCluster(cfg JobConfig) (*Client, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, nil //nolint:nilerr // deliberate: see doc comment
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes client: %w", err)
	}
	return &Client{clientset: clientset, cfg: cfg}, nil
}

const workVolume = "work"
const workMountPath = "/work"
const outputPath = workMountPath + "/basemap.pmtiles"

// runAsNonRootUser is an arbitrary non-zero UID, not the images' own —
// confirmed via `docker run --user 1000:1000` that both protomaps/go-pmtiles
// and alpine/k8s start and run correctly under it. Explicit, rather than
// runAsNonRoot: true alone: that combination is exactly what made the
// pre-promotion AnalysisTemplate's curl container fail outright
// ("cannot verify user is non-root") against an image whose own USER is a
// non-numeric name kubelet cannot statically verify — an explicit numeric
// UID here sidesteps that regardless of what either image declares.
const runAsNonRootUser = 1000

// Trigger creates the update Job and returns its generated name.
func (c *Client) Trigger(ctx context.Context, bbox BBox, maxZoom int, buildDate string) (string, error) {
	falseVal := false
	trueVal := true
	uid := int64(runAsNonRootUser)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "domestique-basemap-update-",
			Namespace:    c.cfg.TilesNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "domestique",
				"domestique.dev/purpose":       "basemap-update",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:          ptr(int32(0)),
			ActiveDeadlineSeconds: ptr(c.cfg.ActiveDeadlineSeconds),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: c.cfg.CopyServiceAccount,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &trueVal,
						RunAsUser:    &uid,
						RunAsGroup:   &uid,
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:  "extract",
							Image: c.cfg.ExtractImage,
							// Direct argv, not a shell — bbox/buildDate are
							// formatted from validated floats/ints, never
							// string-concatenated into something a shell
							// would reinterpret, so there is nothing here
							// for an admin-supplied bbox to inject into.
							Args: []string{
								"extract",
								"https://build.protomaps.com/" + buildDate + ".pmtiles",
								outputPath,
								fmt.Sprintf("--bbox=%g,%g,%g,%g", bbox.West, bbox.South, bbox.East, bbox.North),
								fmt.Sprintf("--maxzoom=%d", maxZoom),
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &falseVal,
								ReadOnlyRootFilesystem:   &trueVal,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(c.cfg.CPURequest),
									corev1.ResourceMemory: resource.MustParse(c.cfg.MemRequest),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse(c.cfg.MemLimit),
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: workVolume, MountPath: workMountPath},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "copy",
							Image:   c.cfg.CopyImage,
							Command: []string{"sh", "-c"},
							// Finds the live tiles pod by label, streams
							// the file in under a .new name, then renames
							// it in place inside that pod — mv on the same
							// filesystem is atomic, so nginx never serves
							// a half-copied file mid-transfer. kubectl
							// picks up in-cluster auth automatically from
							// this container's own mounted ServiceAccount
							// token; no kubeconfig to wire up.
							Args: []string{
								`set -eu
pod=$(kubectl get pod -n "$TILES_NAMESPACE" -l "$TILES_POD_SELECTOR" -o jsonpath='{.items[0].metadata.name}')
if [ -z "$pod" ]; then
  echo "no tiles pod found matching $TILES_POD_SELECTOR in $TILES_NAMESPACE" >&2
  exit 1
fi
kubectl cp "$WORK_FILE" "$TILES_NAMESPACE/$pod:/usr/share/nginx/html/tiles/basemap.pmtiles.new"
kubectl exec -n "$TILES_NAMESPACE" "$pod" -- mv /usr/share/nginx/html/tiles/basemap.pmtiles.new /usr/share/nginx/html/tiles/basemap.pmtiles
size=$(wc -c < "$WORK_FILE" | tr -d ' ')
echo "placed basemap on pod $pod"
echo "SIZE_BYTES=$size"
`,
							},
							Env: []corev1.EnvVar{
								{Name: "TILES_NAMESPACE", Value: c.cfg.TilesNamespace},
								{Name: "TILES_POD_SELECTOR", Value: c.cfg.TilesPodSelector},
								{Name: "WORK_FILE", Value: outputPath},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &falseVal,
								ReadOnlyRootFilesystem:   &trueVal,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: workVolume, MountPath: workMountPath, ReadOnly: true},
								{Name: "tmp", MountPath: "/tmp"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: workVolume,
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{
									SizeLimit: ptrQuantity(resource.MustParse(c.cfg.WorkVolumeSize)),
								},
							},
						},
						{
							Name:         "tmp",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
				},
			},
		},
	}

	created, err := c.clientset.BatchV1().Jobs(c.cfg.TilesNamespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create basemap update job: %w", err)
	}
	return created.Name, nil
}

// JobOutcome is what became of a triggered Job, for the status endpoint to
// report without a caller needing to know anything about Kubernetes.
type JobOutcome struct {
	// Done is false while the Job is still running.
	Done bool
	// Succeeded is only meaningful when Done is true.
	Succeeded bool
	// Message explains a failure. Empty on success.
	Message string
	// SizeBytes is the placed archive's size, parsed from the copy
	// container's own SIZE_BYTES= log line. Only meaningful on success;
	// zero (not an error) if the line could not be read or parsed —
	// logs are best-effort, and a missing size is not worth failing an
	// otherwise-successful update over.
	SizeBytes int64
}

// Outcome reports what happened to a previously triggered Job.
func (c *Client) Outcome(ctx context.Context, jobName string) (JobOutcome, error) {
	job, err := c.clientset.BatchV1().Jobs(c.cfg.TilesNamespace).Get(ctx, jobName, metav1.GetOptions{})
	if err != nil {
		return JobOutcome{}, fmt.Errorf("get basemap update job: %w", err)
	}

	switch {
	case job.Status.Succeeded > 0:
		log := c.copyContainerLog(ctx, jobName)
		return JobOutcome{Done: true, Succeeded: true, SizeBytes: parseSizeBytes(log)}, nil
	case job.Status.Failed > 0:
		log := c.copyContainerLog(ctx, jobName)
		msg := lastNonEmptyLine(log)
		if msg == "" {
			msg = "the job failed — see its pod logs for details"
		}
		return JobOutcome{Done: true, Succeeded: false, Message: msg}, nil
	default:
		return JobOutcome{Done: false}, nil
	}
}

// copyContainerLog reads the copy container's own log for whichever pod the
// Job ran, best-effort — a status report is still useful without it, so a
// log-read failure here is swallowed rather than surfaced as this call's
// own error.
func (c *Client) copyContainerLog(ctx context.Context, jobName string) string {
	pods, err := c.clientset.CoreV1().Pods(c.cfg.TilesNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	req := c.clientset.CoreV1().Pods(c.cfg.TilesNamespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{
		Container: "copy",
		TailLines: ptr(int64(10)),
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return ""
	}
	defer func() { _ = stream.Close() }()
	buf := make([]byte, 4096)
	n, _ := stream.Read(buf)
	return string(buf[:n])
}

// parseSizeBytes finds the copy script's own `SIZE_BYTES=<n>` line.
func parseSizeBytes(log string) int64 {
	for _, line := range strings.Split(log, "\n") {
		if n, ok := strings.CutPrefix(line, "SIZE_BYTES="); ok {
			if v, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
				return v
			}
		}
	}
	return 0
}

func lastNonEmptyLine(log string) string {
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return strings.TrimSpace(lines[i])
		}
	}
	return ""
}

func ptr[T any](v T) *T { return &v }

func ptrQuantity(q resource.Quantity) *resource.Quantity { return &q }
