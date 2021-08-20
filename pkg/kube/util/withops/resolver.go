// Package withops resolves the with ops manifest, by applying ops files and fetching all implicit variables
package withops

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/SUSE/go-patch/patch"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bdm "code.cloudfoundry.org/quarks-operator/pkg/bosh/manifest"
	bdv1 "code.cloudfoundry.org/quarks-operator/pkg/kube/apis/boshdeployment/v1alpha1"
	"code.cloudfoundry.org/quarks-operator/pkg/kube/util/boshdns"
	"code.cloudfoundry.org/quarks-operator/pkg/kube/util/names"
	qsv1a1 "code.cloudfoundry.org/quarks-secret/pkg/kube/apis/quarkssecret/v1alpha1"
	"code.cloudfoundry.org/quarks-utils/pkg/ctxlog"
	"code.cloudfoundry.org/quarks-utils/pkg/logger"
	"code.cloudfoundry.org/quarks-utils/pkg/versionedsecretstore"
	boshtpl "github.com/cloudfoundry/bosh-cli/director/template"
)

// Resolver resolves references from bdpl CR to a BOSH manifest
type Resolver struct {
	client               client.Client
	versionedSecretStore versionedsecretstore.VersionedSecretStore
	newInterpolatorFunc  NewInterpolatorFunc
}

// NewInterpolatorFunc returns a fresh Interpolator
type NewInterpolatorFunc func() Interpolator

// NewResolver constructs a resolver
func NewResolver(client client.Client, f NewInterpolatorFunc) *Resolver {
	return &Resolver{
		client:               client,
		newInterpolatorFunc:  f,
		versionedSecretStore: versionedsecretstore.NewVersionedSecretStore(client),
	}
}

func (r *Resolver) load(ctx context.Context, bdpl *bdv1.BOSHDeployment, namespace string) (*bdm.Manifest, error) {
	var (
		m            string
		err          error
		interpolator = r.newInterpolatorFunc()
		spec         = bdpl.Spec
	)

	m, err = r.resourceData(ctx, namespace, spec.Manifest.Type, spec.Manifest.Name, bdv1.ManifestSpecName)
	if err != nil {
		return nil, errors.Wrapf(err, "Interpolation failed for bosh deployment '%s' in '%s'", bdpl.Name, namespace)
	}

	// Interpolate manifest with ops
	ops := spec.Ops

	for _, op := range ops {
		opsData, err := r.resourceData(ctx, namespace, op.Type, op.Name, bdv1.OpsSpecName)
		if err != nil {
			return nil, errors.Wrapf(err, "Interpolation failed for bosh deployment '%s' in '%s'", bdpl.Name, namespace)
		}
		err = interpolator.AddOps([]byte(opsData))
		if err != nil {
			return nil, errors.Wrapf(err, "Interpolation failed for bosh deployment '%s' in '%s'", bdpl.Name, namespace)
		}
	}

	bytes := []byte(m)
	if len(ops) != 0 {
		bytes, err = interpolator.Interpolate([]byte(m))
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to interpolate %#v in interpolation task", m)
		}
	}

	manifest, err := bdm.LoadYAML(bytes)
	if err != nil {
		return nil, errors.Wrapf(err, "Loading yaml failed in interpolation task after applying ops %#v", m)
	}
	return manifest, nil
}

// Manifest returns manifest and a list of implicit variables referenced by our bdpl CRD
// The resulting manifest has variables interpolated and ops files applied.
// It is the 'with-ops' manifest.
func (r *Resolver) Manifest(ctx context.Context, bdpl *bdv1.BOSHDeployment, namespace string) (*bdm.Manifest, error) {
	manifest, err := r.load(ctx, bdpl, namespace)
	if err != nil {
		return nil, err
	}
	// add to each instance group a labels for each used release to force the bpm controller
	// updating sts and pods if a release changes
	err = manifest.AddReleasesLabels()
	if err != nil {
		return nil, err
	}
	return r.applyVariables(ctx, bdpl, namespace, manifest, "manifest-addons")
}

// ImplicitVariables returns the implicit variables found in the manifest
func (r *Resolver) ImplicitVariables(ctx context.Context, bdpl *bdv1.BOSHDeployment, namespace string) ([]string, error) {
	manifest, err := r.load(ctx, bdpl, namespace)
	if err != nil {
		return nil, err
	}

	refs, err := buildSecretRefs(manifest)
	if err != nil {
		return []string{}, errors.Wrapf(err, "failed to parse all implicit variable names")
	}

	varSecrets := []string{}
	for secName, infos := range refs {
		for _, info := range infos {
			if info.key == "value" {
				varSecrets = append(varSecrets, secName)
			} else {
				varSecrets = append(varSecrets, secName+"/"+info.key)

			}
		}
	}

	return varSecrets, nil
}

// ManifestDetailed returns manifest and a list of implicit variables referenced by our bdpl CRD
// The resulting manifest has variables interpolated and ops files applied.
// It is the 'with-ops' manifest. This variant processes each ops file individually, so it's more debuggable - but slower.
func (r *Resolver) ManifestDetailed(ctx context.Context, bdpl *bdv1.BOSHDeployment, namespace string) (*bdm.Manifest, error) {
	var (
		m    string
		err  error
		spec = bdpl.Spec
	)

	m, err = r.resourceData(ctx, namespace, spec.Manifest.Type, spec.Manifest.Name, bdv1.ManifestSpecName)
	if err != nil {
		return nil, errors.Wrapf(err, "Interpolation failed for bosh deployment %s", namespace)
	}

	// Interpolate manifest with ops
	ops := spec.Ops
	bytes := []byte(m)

	for _, op := range ops {
		interpolator := r.newInterpolatorFunc()

		opsData, err := r.resourceData(ctx, namespace, op.Type, op.Name, bdv1.OpsSpecName)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to get resource data for interpolation of bosh deployment '%s' and ops '%s' in '%s'", bdpl.Name, op.Name, namespace)
		}
		err = interpolator.AddOps([]byte(opsData))
		if err != nil {
			return nil, errors.Wrapf(err, "Interpolation failed for bosh deployment '%s' and ops '%s' in '%s'", bdpl.Name, op.Name, namespace)
		}

		bytes, err = interpolator.Interpolate(bytes)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to interpolate ops '%s' for manifest '%s' in '%s'", op.Name, bdpl.Name, namespace)
		}
	}

	manifest, err := bdm.LoadYAML(bytes)
	if err != nil {
		return nil, errors.Wrapf(err, "Loading yaml failed in interpolation task after applying ops %#v", m)
	}

	// add to each instance group a labels for each used release
	err = manifest.AddReleasesLabels()
	if err != nil {
		return nil, errors.Wrapf(err, "Add labels for releases failed after interpolation: %#v", m)
	}

	manifest, err = r.applyVariables(ctx, bdpl, namespace, manifest, "detailed-manifest-addons")
	if err != nil {
		return nil, errors.Wrapf(err, "Loading yaml failed after applying variable: %#v", m)
	}
	return manifest, nil
}

type secretInfo struct {
	key      string
	variable string
}

// secretRefs references secrets and keys. It also stores the original variable usage (name/key).
// If the variable has no slash the default key is 'value', so 'name/value' is identical to just 'name'.
type secretRefs map[string][]secretInfo

func (s secretRefs) add(variable string, secName string, key string) {
	si := s[secName]
	si = append(si, secretInfo{variable: variable, key: key})
	s[secName] = si
}

// Find implicit variable references and index by secret name
func buildSecretRefs(manifest *bdm.Manifest) (secretRefs, error) {
	vars, err := manifest.ImplicitVariables()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to list implicit variables")
	}

	refs := make(secretRefs, len(vars))
	for _, v := range vars {
		key := ""
		secName := ""
		// implicit variables can have a slash to specify the key in the secret
		if bdm.SlashedVariable(v) {
			parts := strings.Split(v, "/")
			if len(parts) != 2 {
				return refs, fmt.Errorf("expected one / separator for implicit variable/key name, have %d", len(parts))
			}

			secName = names.SecretVariableName(parts[0])
			key = parts[1]
		} else {
			secName = names.SecretVariableName(v)
			key = bdv1.ImplicitVariableKeyName
		}

		refs.add(v, secName, key)
	}
	return refs, nil
}

// Apply all variables and interpolate
func (r *Resolver) applyVariables(ctx context.Context, bdpl *bdv1.BOSHDeployment, namespace string, manifest *bdm.Manifest, logName string) (*bdm.Manifest, error) {
	refs, err := buildSecretRefs(manifest)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse all implicit variable names")
	}

	// fetch each secret for implicit variables
	impVars := boshtpl.StaticVariables{}
	for secName, infos := range refs {
		secret := &corev1.Secret{}
		err := r.client.Get(ctx, types.NamespacedName{Name: secName, Namespace: namespace}, secret)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get secret '%s/%s'", namespace, secName)
		}

		for _, info := range infos {
			val, ok := secret.Data[info.key]
			if !ok {
				return nil, fmt.Errorf("secret '%s/%s' doesn't contain key '%s' for variable '%s'", namespace, secName, info.key, info.variable)
			}

			if t, ok := secret.Annotations[bdv1.AnnotationJSONValue]; ok && t == "true" {
				var js interface{}
				err := json.Unmarshal(val, &js)
				if err != nil {
					return nil, errors.Wrapf(err, "failed to unmarshal JSON in '%s' from secret '%s/%s'", info.variable, namespace, secName)
				}
				impVars[info.variable] = js
			} else {
				impVars[info.variable] = string(val)
			}

		}
	}

	// Interpolate variables
	boshManifestBytes, _ := manifest.Marshal()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal manifest")
	}
	tpl := boshtpl.NewTemplate(boshManifestBytes)
	evalOpts := boshtpl.EvaluateOpts{ExpectAllKeys: false, ExpectAllVarsUsed: false}
	yamlBytes, err := tpl.Evaluate(impVars, patch.Ops{}, evalOpts)
	if err != nil {
		return nil, errors.Wrapf(err, "could not evaluate variables")
	}

	manifest, err = bdm.LoadYAML(yamlBytes)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to load manifest with evaluated variables")
	}

	// Apply addons
	log := ctxlog.ExtractLogger(ctx)
	err = manifest.ApplyAddons(logger.TraceFilter(log, logName))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to apply addons")
	}

	// Interpolate user-provided explicit variables
	bytes, err := manifest.Marshal()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal bdpl '%s/%s' after applying addons", bdpl.Namespace, bdpl.Name)
	}

	var userVars []boshtpl.Variables
	for _, userVar := range bdpl.Spec.Vars {
		varName := userVar.Name
		varSecretName := userVar.Secret
		secret := &corev1.Secret{}
		err := r.client.Get(ctx, types.NamespacedName{Name: varSecretName, Namespace: namespace}, secret)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to retrieve secret '%s/%s' via client.Get", namespace, varSecretName)
		}
		staticVars := boshtpl.StaticVariables{}
		for key, varBytes := range secret.Data {
			switch key {
			case "password":
				staticVars[varName] = string(varBytes)
			default:
				staticVars[varName] = MergeStaticVar(staticVars[varName], key, string(varBytes))
			}
		}
		userVars = append(userVars, staticVars)
	}

	bytes, err = InterpolateExplicitVariables(bytes, userVars, false)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to interpolate user provided explicit variables manifest '%s' in '%s'", bdpl.Name, namespace)
	}

	manifest, err = bdm.LoadYAML(bytes)
	if err != nil {
		return nil, errors.Wrapf(err, "Loading yaml failed in interpolation task after applying user explicit vars")
	}

	err = boshdns.Validate(*manifest)
	if err != nil {
		return nil, err
	}
	manifest.ApplyUpdateBlock()

	return manifest, err
}

// resourceData resolves different manifest reference types and returns the resource's data
func (r *Resolver) resourceData(ctx context.Context, namespace string, resType bdv1.ReferenceType, name string, key string) (string, error) {
	var (
		data string
		ok   bool
	)

	switch resType {
	case bdv1.ConfigMapReference:
		opsConfig := &corev1.ConfigMap{}
		err := r.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, opsConfig)
		if err != nil {
			return data, errors.Wrapf(err, "failed to retrieve %s from configmap '%s/%s' via client.Get", key, namespace, name)
		}
		data, ok = opsConfig.Data[key]
		if !ok {
			return data, fmt.Errorf("configMap '%s/%s' doesn't contain key '%s'", namespace, name, key)
		}
	case bdv1.SecretReference:
		opsSecret := &corev1.Secret{}
		err := r.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, opsSecret)
		if err != nil {
			return data, errors.Wrapf(err, "failed to retrieve %s from secret '%s/%s' via client.Get", key, namespace, name)
		}
		encodedData, ok := opsSecret.Data[key]
		if !ok {
			return data, fmt.Errorf("secret '%s/%s' doesn't contain key '%s'", namespace, name, key)
		}
		data = string(encodedData)
	case bdv1.URLReference:
		httpResponse, err := http.Get(name)
		if err != nil {
			return data, errors.Wrapf(err, "failed to resolve %s from url '%s' via http.Get", key, name)
		}
		body, err := ioutil.ReadAll(httpResponse.Body)
		if err != nil {
			return data, errors.Wrapf(err, "failed to read %s response body '%s' via ioutil", key, name)
		}
		data = string(body)
	default:
		return data, fmt.Errorf("unrecognized %s ref type %s", key, name)
	}

	return data, nil
}

// InterpolateVariableFromSecrets reads explicit secrets and writes an interpolated manifest into desired manifest secret.
func (r *Resolver) InterpolateVariableFromSecrets(ctx context.Context, withOpsManifestData []byte, namespace string, boshdeploymentName string) ([]byte, error) {
	var vars []boshtpl.Variables

	withOpsManifest, err := bdm.LoadYAML(withOpsManifestData)
	if err != nil {
		return nil, err
	}

	for _, variable := range withOpsManifest.Variables {
		staticVars := boshtpl.StaticVariables{}

		varName := variable.Name
		varSecretName := names.SecretVariableName(varName)

		varQuarksSecret := &qsv1a1.QuarksSecret{}
		err = r.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: varSecretName}, varQuarksSecret)
		if err != nil {
			return nil, err
		}

		if !varQuarksSecret.Status.IsGenerated() {
			return nil, errors.Errorf("QuarksSecret '%s' has generated status false", varQuarksSecret.Name)
		}

		varSecret := &corev1.Secret{}
		err = r.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: varSecretName}, varSecret)
		if err != nil {
			return nil, err
		}

		varSecretData := varSecret.Data
		for key, value := range varSecretData {
			switch key {
			case "password":
				staticVars[varName] = string(value)
			default:
				staticVars[varName] = MergeStaticVar(staticVars[varName], key, string(value))
			}
		}
		vars = append(vars, staticVars)
	}
	desiredManifestBytes, err := InterpolateExplicitVariables(withOpsManifestData, vars, true)
	if err != nil {
		return nil, errors.Wrap(err, "failed to interpolate explicit variables")
	}

	return desiredManifestBytes, nil
}

// InterpolateExplicitVariables interpolates explicit variables in the manifest
// Expects an array of maps, each element being a variable: [{ "name":"foo", "password": "value" }, {"name": "bar", "ca": "---"} ]
// Returns the new manifest as a byte array
func InterpolateExplicitVariables(boshManifestBytes []byte, vars []boshtpl.Variables, expectAllKeys bool) ([]byte, error) {
	multiVars := boshtpl.NewMultiVars(vars)
	tpl := boshtpl.NewTemplate(boshManifestBytes)

	// Following options are empty for quarks-operator
	op := patch.Ops{}
	evalOpts := boshtpl.EvaluateOpts{
		ExpectAllKeys:     expectAllKeys,
		ExpectAllVarsUsed: false,
	}

	yamlBytes, err := tpl.Evaluate(multiVars, op, evalOpts)
	if err != nil {
		return nil, errors.Wrapf(err, "could not evaluate variables")
	}

	m, err := bdm.LoadYAML(yamlBytes)
	if err != nil {
		return nil, errors.Wrapf(err, "could not evaluate variables")
	}

	yamlBytes, err = m.Marshal()
	if err != nil {
		return nil, errors.Wrapf(err, "could not evaluate variables")
	}

	return yamlBytes, nil
}

// MergeStaticVar builds a map of values used for BOSH explicit variable interpolation
func MergeStaticVar(staticVar interface{}, field string, value string) interface{} {
	if staticVar == nil {
		staticVar = map[interface{}]interface{}{
			field: value,
		}
	} else {
		staticVarMap := staticVar.(map[interface{}]interface{})
		staticVarMap[field] = value
		staticVar = staticVarMap
	}

	return staticVar
}
