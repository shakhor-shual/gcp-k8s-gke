package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/gcp"
	"github.com/gruntwork-io/terratest/modules/helm"
	http_helper "github.com/gruntwork-io/terratest/modules/http-helper"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/logger"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/gruntwork-io/terratest/modules/shell"
	"github.com/gruntwork-io/terratest/modules/terraform"
	test_structure "github.com/gruntwork-io/terratest/modules/test-structure"
	"github.com/stretchr/testify/require"
)

func TestGKEBasicTiller(t *testing.T) {
	t.Parallel()

	// Uncomment any of the following to skip that section during the test
	// os.Setenv("SKIP_create_test_copy_of_examples", "true")
	// os.Setenv("SKIP_create_terratest_options", "true")
	// os.Setenv("SKIP_terraform_apply", "true")
	// os.Setenv("SKIP_configure_kubectl", "true")
	// os.Setenv("SKIP_wait_for_workers", "true")
	// os.Setenv("SKIP_helm_install", "true")
	// os.Setenv("SKIP_cleanup", "true")

	// Create a directory path that won't conflict
	workingDir := filepath.Join(".", "stages", t.Name())

	test_structure.RunTestStage(t, "create_test_copy_of_examples", func() {
		testFolder := test_structure.CopyTerraformFolderToTemp(t, "..", "examples")
		logger.Logf(t, "path to test folder %s\n", testFolder)
		terraformModulePath := filepath.Join(testFolder, "gke-basic-tiller")
		test_structure.SaveString(t, workingDir, "gkeBasicTillerTerraformModulePath", terraformModulePath)
	})

	test_structure.RunTestStage(t, "create_terratest_options", func() {
		gkeBasicTillerTerraformModulePath := test_structure.LoadString(t, workingDir, "gkeBasicTillerTerraformModulePath")
		tmpKubeConfigPath := k8s.CopyHomeKubeConfigToTemp(t)
		kubectlOptions := k8s.NewKubectlOptions("", tmpKubeConfigPath)
		uniqueID := random.UniqueId()
		project := gcp.GetGoogleProjectIDFromEnvVar(t)
		region := gcp.GetRandomRegion(t, project, nil, nil)
		gkeClusterTerratestOptions := createGKEClusterTerraformOptions(t, uniqueID, project, region,
			gkeBasicTillerTerraformModulePath, tmpKubeConfigPath)
		test_structure.SaveString(t, workingDir, "uniqueID", uniqueID)
		test_structure.SaveString(t, workingDir, "project", project)
		test_structure.SaveString(t, workingDir, "region", region)
		test_structure.SaveTerraformOptions(t, workingDir, gkeClusterTerratestOptions)
		test_structure.SaveKubectlOptions(t, workingDir, kubectlOptions)
	})

	defer test_structure.RunTestStage(t, "cleanup", func() {
		gkeClusterTerratestOptions := test_structure.LoadTerraformOptions(t, workingDir)
		terraform.Destroy(t, gkeClusterTerratestOptions)

		kubectlOptions := test_structure.LoadKubectlOptions(t, workingDir)
		err := os.Remove(kubectlOptions.ConfigPath)
		require.NoError(t, err)
	})

	test_structure.RunTestStage(t, "terraform_apply", func() {
		gkeClusterTerratestOptions := test_structure.LoadTerraformOptions(t, workingDir)
		terraform.InitAndApply(t, gkeClusterTerratestOptions)
	})

	test_structure.RunTestStage(t, "configure_kubectl", func() {
		gkeClusterTerratestOptions := test_structure.LoadTerraformOptions(t, workingDir)
		kubectlOptions := test_structure.LoadKubectlOptions(t, workingDir)
		project := test_structure.LoadString(t, workingDir, "project")
		region := test_structure.LoadString(t, workingDir, "region")
		clusterName := gkeClusterTerratestOptions.Vars["cluster_name"].(string)

		// gcloud beta container clusters get-credentials example-cluster --region australia-southeast1 --project dev-sandbox-123456
		cmd := shell.Command{
			Command: "gcloud",
			Args:    []string{"beta", "container", "clusters", "get-credentials", clusterName, "--region", region, "--project", project},
			Env: map[string]string{
				"KUBECONFIG": kubectlOptions.ConfigPath,
			},
		}

		shell.RunCommand(t, cmd)
	})

	test_structure.RunTestStage(t, "wait_for_workers", func() {
		kubectlOptions := test_structure.LoadKubectlOptions(t, workingDir)
		verifyGkeNodesAreReady(t, kubectlOptions)
	})

	test_structure.RunTestStage(t, "helm_install", func() {
		// Path to the helm chart we will test
		helmChartPath := "charts/minimal-pod"

		// Load the temporary kubectl config file and use its current context
		// We also specify that we are working in the default namespace (required to get the Pod)
		kubectlOptions := test_structure.LoadKubectlOptions(t, workingDir)
		kubectlOptions.Namespace = "default"

		// We generate a unique release name so that we can refer to after deployment.
		// By doing so, we can schedule the delete call here so that at the end of the test, we run
		// `helm delete RELEASE_NAME` to clean up any resources that were created.
		releaseName := fmt.Sprintf("nginx-%s", strings.ToLower(random.UniqueId()))

		// Setup the args. For this test, we will set the following input values:
		// - image=nginx:1.15.8
		// - fullnameOverride=minimal-pod-RANDOM_STRING
		// We use a fullnameOverride so we can find the Pod later during verification
		podName := fmt.Sprintf("%s-minimal-pod", releaseName)
		options := &helm.Options{
			SetValues: map[string]string{
				"image":            "nginx:1.15.8",
				"fullnameOverride": podName,
			},
			EnvVars: map[string]string{
				"HELM_TLS_VERIFY": "true",
				"HELM_TLS_ENABLE": "true",
			},
		}

		// Deploy the chart using `helm install`. Note that we use the version without `E`, since we want to assert the
		// install succeeds without any errors.
		helm.Install(t, options, helmChartPath, releaseName)

		// Now that the chart is deployed, verify the deployment. This function will open a tunnel to the Pod and hit the
		// nginx container endpoint.
		verifyNginxPod(t, kubectlOptions, podName)
	})
}

// verifyNginxPod will open a tunnel to the Pod and hit the endpoint to verify the nginx welcome page is shown.
func verifyNginxPod(t *testing.T, kubectlOptions *k8s.KubectlOptions, podName string) {
	// Wait for the pod to come up. It takes some time for the Pod to start, so retry a few times.
	retries := 15
	sleep := 5 * time.Second
	k8s.WaitUntilPodAvailable(t, kubectlOptions, podName, retries, sleep)

	// We will first open a tunnel to the pod, making sure to close it at the end of the test.
	tunnel := k8s.NewTunnel(kubectlOptions, k8s.ResourceTypePod, podName, 0, 80)
	defer tunnel.Close()
	tunnel.ForwardPort(t)

	// ... and now that we have the tunnel, we will verify that we get back a 200 OK with the nginx welcome page.
	// It takes some time for the Pod to start, so retry a few times.
	endpoint := fmt.Sprintf("http://%s", tunnel.Endpoint())
	http_helper.HttpGetWithRetryWithCustomValidation(
		t,
		endpoint,
		retries,
		sleep,
		func(statusCode int, body string) bool {
			return statusCode == 200 && strings.Contains(body, "Welcome to nginx")
		},
	)
}
