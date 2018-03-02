/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"fmt"
	"net"

	"github.com/spf13/cobra"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apiserver/pkg/admission"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/sample-apiserver/pkg/admission/plugin/banflunder"
	"k8s.io/sample-apiserver/pkg/admission/wardleinitializer"
	"k8s.io/sample-apiserver/pkg/apis/wardle/v1alpha1"
	"k8s.io/sample-apiserver/pkg/apiserver"
	clientset "k8s.io/sample-apiserver/pkg/client/clientset/internalversion"
	informers "k8s.io/sample-apiserver/pkg/client/informers/internalversion"
)

const defaultEtcdPathPrefix = "/registry/wardle.kubernetes.io"

type WardleServerOptions struct {
	// Default apiserver options
	RecommendedOptions *genericoptions.RecommendedOptions

	SharedInformerFactory informers.SharedInformerFactory
}

func NewWardleServerOptions() *WardleServerOptions {
	// Both parameters are for etcd storage default options.
	options := genericoptions.NewRecommendedOptions(defaultEtcdPathPrefix, apiserver.Codecs.LegacyCodec(v1alpha1.SchemeGroupVersion))

	return &WardleServerOptions{
		RecommendedOptions: options,
	}
}

// NewCommandStartWardleServer provides a CLI handler for 'start master' command
// with a default WardleServerOptions.
func NewCommandStartWardleServer(defaults *WardleServerOptions, stopCh <-chan struct{}) *cobra.Command {
	o := *defaults

	cmd := &cobra.Command{
		Short: "Launch a wardle API server",
		Long:  "Launch a wardle API server",
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.Complete(); err != nil {
				return err
			}
			if err := o.Validate(args); err != nil {
				return err
			}
			if err := o.RunWardleServer(stopCh); err != nil {
				return err
			}
			return nil
		},
	}

	flags := cmd.Flags()
	o.RecommendedOptions.AddFlags(flags)

	return cmd
}

func (o WardleServerOptions) Validate(args []string) error {
	// No need to copy errors
	// errors := append([]error{}, o.RecommendedOptions.Validate()...)

	errors := o.RecommendedOptions.Validate()

	return utilerrors.NewAggregate(errors)
}

func (o *WardleServerOptions) Complete() error {
	return nil
}

func (o *WardleServerOptions) Config() (*apiserver.Config, error) {
	// register admission plugins
	banflunder.Register(o.RecommendedOptions.Admission.Plugins)

	// TODO have a "real" external address
	// Generaate self signed certificate if necessary
	if err := o.RecommendedOptions.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		return nil, fmt.Errorf("error creating self-signed certificates: %v", err)
	}

	// The callback is called after all ApplyTo is done. It configures o.SharedInformerFactory and
	// a admission plugin initializer.
	o.RecommendedOptions.ExtraAdmissionInitializers = func(c *genericapiserver.RecommendedConfig) ([]admission.PluginInitializer, error) {
		client, err := clientset.NewForConfig(c.LoopbackClientConfig)
		if err != nil {
			return nil, err
		}
		informerFactory := informers.NewSharedInformerFactory(client, c.LoopbackClientConfig.Timeout)
		o.SharedInformerFactory = informerFactory
		return []admission.PluginInitializer{wardleinitializer.New(informerFactory)}, nil
	}

	// RecommendedConfig is configured by RecommendedOptions, plus predefined Codecs and Schema
	serverConfig := genericapiserver.NewRecommendedConfig(apiserver.Codecs)
	if err := o.RecommendedOptions.ApplyTo(serverConfig, apiserver.Scheme); err != nil {
		return nil, err
	}

	// ExtraConfig is all other configurations not configured by recommended config. It's
	// empty now. GenericConfig is *genericapiserver.RecommendedConfig.
	config := &apiserver.Config{
		GenericConfig: serverConfig,
		ExtraConfig:   apiserver.ExtraConfig{},
	}
	return config, nil
}

func (o WardleServerOptions) RunWardleServer(stopCh <-chan struct{}) error {
	// config is *genericapiserver.RecommendedConfig
	config, err := o.Config()
	if err != nil {
		return err
	}

	// When config.Complete() it becomes genericapiserver.CompletedConfig. CompleteConfig.New()
	// returns a server. API groups need to be installed in the server.
	server, err := config.Complete().New()
	if err != nil {
		return err
	}

	// This post hook will start informers.
	server.GenericAPIServer.AddPostStartHook("start-sample-server-informers", func(context genericapiserver.PostStartHookContext) error {
		config.GenericConfig.SharedInformerFactory.Start(context.StopCh)
		return nil
	})

	// Prepare and run the api server
	return server.GenericAPIServer.PrepareRun().Run(stopCh)
}
