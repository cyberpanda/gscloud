package cmd

import (
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientauth "k8s.io/client-go/pkg/apis/clientauthentication/v1beta1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"gitlab.com/gridscale/gscloud/endpoints"
)

// clusterCmd represents the cluster command
var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "cluster command handles actions that preformed on a kubernetes cluster",
	Long:  "cluster command handles actions that preformed on a kubernetes cluster",
}

// kubernetesCmd represents the kubernetes command
var kubernetesCmd = &cobra.Command{
	Use:   "kubernetes",
	Short: "kubernetes command handles actions that preformed on the managed kubernetes service",
	Long:  "kubernetes command handles actions that preformed on the managed kubernetes service",
}

// saveKubeconfigCmd represents the kubeconfig command
var saveKubeconfigCmd = &cobra.Command{
	Use:   "save-kubeconfig",
	Short: "Saves configuration of the given cluster into the provided kubeconfig",
	Long:  "Saves configuration of the given cluster into the provided kubeconfig or KUBECONFIG ENV. variable",
	Run: func(cmd *cobra.Command, args []string) {
		kubeConfigFile, _ := cmd.Flags().GetString("kubeconfig")
		clusterID, _ := cmd.Flags().GetString("cluster")
		credentialPlugin, _ := cmd.Flags().GetBool("credential-plugin")
		kubeConfigEnv := os.Getenv("KUBECONFIG")

		pathOptions := clientcmd.NewDefaultPathOptions()
		if kubeConfigFile != "" {
			kubeConfigEnv = kubeConfigFile
			pathOptions.GlobalFile = kubeConfigFile
		}

		if kubeConfigEnv != "" && !fileExists(kubeConfigEnv) {
			_, err := os.Create(kubeConfigEnv)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}

		currentKubeConfig, err := pathOptions.GetStartingConfig()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		newKubeConfig := fetchKubeConfigFromProvider(clusterID)
		if len(newKubeConfig.Clusters) == 0 || len(newKubeConfig.Users) == 0 {
			fmt.Fprintln(os.Stderr, "Error: Invaild kubeconfig")
			os.Exit(1)
		}
		c := newKubeConfig.Clusters[0]
		u := newKubeConfig.Users[0]

		certificateAuthorityData, err := b64.StdEncoding.DecodeString(c.Cluster.CertificateAuthorityData)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		currentKubeConfig.Clusters[c.Name] = &clientcmdapi.Cluster{
			Server:                   c.Cluster.Server,
			CertificateAuthorityData: certificateAuthorityData,
		}
		currentKubeConfig.AuthInfos[u.Name] = &clientcmdapi.AuthInfo{
			ClientCertificate: u.User.ClientKeyData,
			ClientKey:         u.User.ClientCertificateData,
		}
		if credentialPlugin {
			currentKubeConfig.AuthInfos[u.Name] = &clientcmdapi.AuthInfo{
				Exec: &clientcmdapi.ExecConfig{
					APIVersion: clientauth.SchemeGroupVersion.String(),
					Command:    cliPath(),
					Args: []string{
						"--config",
						cfgFile,
						"--account",
						account,
						"kubernetes",
						"cluster",
						"exec-credential",
						"--cluster",
						clusterID,
					},
				},
			}
		} else {
			clientCertificateData, err := b64.StdEncoding.DecodeString(u.User.ClientCertificateData)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}

			clientKeyData, err := b64.StdEncoding.DecodeString(u.User.ClientKeyData)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}

			currentKubeConfig.AuthInfos[u.Name] = &clientcmdapi.AuthInfo{
				ClientCertificateData: clientCertificateData,
				ClientKeyData:         clientKeyData,
			}
		}

		currentKubeConfig.Contexts[newKubeConfig.CurrentContext] = &clientcmdapi.Context{
			Cluster:  c.Name,
			AuthInfo: u.Name,
		}
		currentKubeConfig.CurrentContext = newKubeConfig.CurrentContext

		err = clientcmd.ModifyConfig(pathOptions, *currentKubeConfig, true)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

	},
}

func init() {
	clusterCmd.AddCommand(saveKubeconfigCmd)
	saveKubeconfigCmd.Flags().String("kubeconfig", "", "(optional) absolute path to the kubeconfig file")
	saveKubeconfigCmd.Flags().String("cluster", "", "The cluster's uuid")
	saveKubeconfigCmd.MarkFlagRequired("cluster")
	saveKubeconfigCmd.Flags().Bool("credential-plugin", false, "Enables credential plugin authentication method (exec-credential)")
}

// execCredentialCmd represents the getCertificate command
var execCredentialCmd = &cobra.Command{
	Use:   "exec-credential",
	Short: "Provides client's credential to kubectl command",
	Long:  "exec-credential provides client's credential to kubectl command",
	Run: func(cmd *cobra.Command, args []string) {
		kubeConfigFile, _ := cmd.Flags().GetString("kubeconfig")
		clusterID, _ := cmd.Flags().GetString("cluster")

		kubectlDefaults := clientcmd.NewDefaultPathOptions()
		if kubeConfigFile != "" {
			kubectlDefaults.GlobalFile = kubeConfigFile
		}

		_, err := kubectlDefaults.GetStartingConfig()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}

		execCredential, err := loadCachedKubeConfig(clusterID)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}

		if execCredential == nil {
			newKubeConfig := fetchKubeConfigFromProvider(clusterID)
			if len(newKubeConfig.Users) != 0 {
				u := newKubeConfig.Users[0]
				clientKeyData, err := b64.StdEncoding.DecodeString(u.User.ClientKeyData)
				if err != nil {
					fmt.Println(err)
				}
				clientCertificateData, err := b64.StdEncoding.DecodeString(u.User.ClientCertificateData)
				if err != nil {
					fmt.Println(err)
				}

				execCredential = &clientauth.ExecCredential{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ExecCredential",
						APIVersion: clientauth.SchemeGroupVersion.String(),
					},
					Status: &clientauth.ExecCredentialStatus{
						ClientKeyData:         string(clientKeyData),
						ClientCertificateData: string(clientCertificateData),
						ExpirationTimestamp:   &metav1.Time{Time: time.Now().Add(time.Hour)},
					},
				}

				if err := cacheKubeConfig(clusterID, execCredential); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			}

		}
		if execCredential == nil {
			fmt.Println("Error: Could not retrieve kubeconfig from provider for account: ", account)
			return
		}
		execCredentialJSON, err := json.MarshalIndent(execCredential, "", "    ")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		// this output will be used by kubectl
		fmt.Println(string(execCredentialJSON))

	},
}

func init() {
	execCredentialCmd.Flags().String("kubeconfig", "", "(optional) absolute path to the kubeconfig file")
	execCredentialCmd.Flags().String("cluster", "", "The cluster's uuid")
	execCredentialCmd.MarkFlagRequired("cluster")
}

func fetchKubeConfigFromProvider(id string) *kubeConfig {
	var kc kubeConfig

	// generate kubeconfig
	r := request{
		uri:    path.Join(apiPaasServiceBase, id, "renew_credentials"),
		method: http.MethodPatch,
		body:   endpoints.PaaSKubeCredentialBody{},
	}
	r.execute(*client, nil)

	// retrieve kubeconfig
	r = request{
		uri:    path.Join(apiPaasServiceBase, id),
		method: http.MethodGet,
		body:   endpoints.PaaSKubeCredentialBody{},
	}

	var paaSService endpoints.PaaSService
	r.execute(*client, &paaSService)

	if len(paaSService.Properties.Credentials) != 0 {
		err := yaml.Unmarshal([]byte(paaSService.Properties.Credentials[0].KubeConfig), &kc)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}

	return &kc
}

func kubeConfigCachePath() string {
	dir, _ := filepath.Abs(filepath.Dir(cfgFile))
	return filepath.Join(dir, "cache", "exec-credential")
}

func cachedKubeConfigPath(id string) string {
	return filepath.Join(kubeConfigCachePath(), id+".json")
}

func cacheKubeConfig(id string, execCredential *clientauth.ExecCredential) error {
	if execCredential.Status.ExpirationTimestamp.IsZero() {
		return nil
	}

	cachePath := kubeConfigCachePath()
	if err := os.MkdirAll(cachePath, os.FileMode(0700)); err != nil {
		return err
	}

	path := cachedKubeConfigPath(id)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(0600))
	if err != nil {
		return err
	}
	defer f.Close()

	return json.NewEncoder(f).Encode(execCredential)
}

func loadCachedKubeConfig(id string) (*clientauth.ExecCredential, error) {
	kubeConfigPath := cachedKubeConfigPath(id)
	f, err := os.Open(kubeConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	defer f.Close()

	var execCredential *clientauth.ExecCredential
	if err := json.NewDecoder(f).Decode(&execCredential); err != nil {
		return nil, err
	}

	timeStamp := execCredential.Status.ExpirationTimestamp

	if execCredential.Status == nil || timeStamp.IsZero() || timeStamp.Time.Before(time.Now()) {
		err = os.Remove(kubeConfigPath)
		return nil, err
	}

	return execCredential, nil
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
