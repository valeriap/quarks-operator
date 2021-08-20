// Package manifest represents a valid BOSH manifest and provides funcs to load
// it, marshal it and access its fields.
package manifest

import (
	"bytes"
	"crypto"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	goyaml "gopkg.in/yaml.v2"

	"sigs.k8s.io/yaml"

	qsv1a1 "code.cloudfoundry.org/quarks-secret/pkg/kube/apis/quarkssecret/v1alpha1"
)

const (
	// DesiredManifestKeyName is the name of the key in desired manifest secret
	DesiredManifestKeyName = "manifest.yaml"
)

// ReleaseImageProvider interface to provide the docker release image for a BOSH job
// This lookup is currently implemented by the manifest model.
type ReleaseImageProvider interface {
	// GetReleaseImage returns the release image for an job in an instance group
	GetReleaseImage(instanceGroupName, jobName string) (string, error)
}

// Feature from BOSH deployment manifest
type Feature struct {
	ConvergeVariables    bool  `json:"converge_variables"`
	RandomizeAzPlacement *bool `json:"randomize_az_placement,omitempty"`
	UseDNSAddresses      *bool `json:"use_dns_addresses,omitempty"`
	UseTmpfsJobConfig    *bool `json:"use_tmpfs_job_config,omitempty"`
}

// AuthType from BOSH deployment manifest
type AuthType string

// InstanceGroupType represents instance groups types
type InstanceGroupType string

// AuthType values from BOSH deployment manifest
const (
	ClientAuth AuthType = "client_auth"
	ServerAuth AuthType = "server_auth"

	IGTypeService    InstanceGroupType = "service"
	IGTypeErrand     InstanceGroupType = "errand"
	IGTypeAutoErrand InstanceGroupType = "auto-errand"
	IGTypeDefault    InstanceGroupType = ""

	// BoshDNSAddOnName name of bosh dns addon.
	BoshDNSAddOnName        = "bosh-dns"
	BOSHDNSAliasesAddOnName = "bosh-dns-aliases"
)

// VariableOptions from BOSH deployment manifest
type VariableOptions struct {
	CommonName                  string                    `json:"common_name"`
	AlternativeNames            []string                  `json:"alternative_names,omitempty"`
	IsCA                        bool                      `json:"is_ca"`
	CA                          string                    `json:"ca,omitempty"`
	ExtendedKeyUsage            []AuthType                `json:"extended_key_usage,omitempty"`
	SignerType                  string                    `json:"signer_type,omitempty"`
	ServiceRef                  []qsv1a1.ServiceReference `json:"serviceRef,omitempty"`
	Copies                      []qsv1a1.Copy             `json:"copies,omitempty"`
	ActivateEKSWorkaroundForSAN bool                      `json:"activateEKSWorkaroundForSAN,omitempty"`
}

// Variable from BOSH deployment manifest
type Variable struct {
	Name    string           `json:"name"`
	Type    string           `json:"type"`
	Options *VariableOptions `json:"options,omitempty"`
}

// Stemcell from BOSH deployment manifest
type Stemcell struct {
	Alias   string `json:"alias"`
	OS      string `json:"os,omitempty"`
	Version string `json:"version"`
	Name    string `json:"name,omitempty"`
}

// ReleaseStemcell from BOSH deployment manifest
type ReleaseStemcell struct {
	OS      string `json:"os"`
	Version string `json:"version"`
}

// Release from BOSH deployment manifest
type Release struct {
	Name     string           `json:"name"`
	Version  string           `json:"version"`
	URL      string           `json:"url,omitempty"`
	SHA1     string           `json:"sha1,omitempty"`
	Stemcell *ReleaseStemcell `json:"stemcell,omitempty"`
}

// AddOnJob from BOSH deployment manifest
type AddOnJob struct {
	Name       string                 `json:"name"`
	Release    string                 `json:"release"`
	Properties JobProperties          `json:"properties,omitempty"`
	Consumes   map[string]interface{} `json:"consumes,omitempty"`
	Provides   map[string]interface{} `json:"provides,omitempty"`
}

// AddOnStemcell from BOSH deployment manifest
type AddOnStemcell struct {
	OS string `json:"os"`
}

// AddOnPlacementJob from BOSH deployment manifest
type AddOnPlacementJob struct {
	Name    string `json:"name"`
	Release string `json:"release"`
}

// AddOnPlacementRules from BOSH deployment manifest
type AddOnPlacementRules struct {
	Stemcell      []*AddOnStemcell     `json:"stemcell,omitempty"`
	Deployments   []string             `json:"deployments,omitempty"`
	Jobs          []*AddOnPlacementJob `json:"release,omitempty"`
	InstanceGroup []string             `json:"instance_groups,omitempty"`
	Networks      []string             `json:"networks,omitempty"`
	Teams         []string             `json:"teams,omitempty"`
	Lifecycle     InstanceGroupType    `json:"lifecycle,omitempty"`
}

// AddOn from BOSH deployment manifest
type AddOn struct {
	Name    string               `json:"name"`
	Jobs    []AddOnJob           `json:"jobs"`
	Include *AddOnPlacementRules `json:"include,omitempty"`
	Exclude *AddOnPlacementRules `json:"exclude,omitempty"`
}

// Manifest is a BOSH deployment manifest
type Manifest struct {
	DirectorUUID   string                 `json:"director_uuid"`
	InstanceGroups InstanceGroups         `json:"instance_groups,omitempty"`
	Features       *Feature               `json:"features,omitempty"`
	Tags           map[string]string      `json:"tags,omitempty"`
	Releases       []*Release             `json:"releases,omitempty"`
	Stemcells      []*Stemcell            `json:"stemcells,omitempty"`
	AddOns         []*AddOn               `json:"addons,omitempty"`
	Properties     map[string]interface{} `json:"properties,omitempty"`
	Variables      []Variable             `json:"variables,omitempty"`
	Update         *Update                `json:"update,omitempty"`
	AddOnsApplied  bool                   `json:"addons_applied,omitempty"`
}

// duplicateYamlValue is a struct used for size compression
// in Marshal function  to store the yaml values of
// significant size and which occur more than once.
type duplicateYamlValue struct {
	Hash          string
	YamlKeyMarker string
}

// LoadYAML returns a new BOSH deployment manifest from a yaml representation
func LoadYAML(data []byte) (*Manifest, error) {
	m := &Manifest{}
	err := yaml.Unmarshal(data, m, func(opt *json.Decoder) *json.Decoder {
		opt.UseNumber()
		return opt
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal BOSH deployment manifest %s", string(data))
	}

	return m, nil
}

// Marshal serializes a BOSH manifest into yaml
func (m *Manifest) Marshal() ([]byte, error) {

	marshalledManifest, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}

	// UnMarshalling the manifest to interface{}interface{} so that it is easy to loop.
	manifestInterfaceMap := goyaml.MapSlice{}
	err = goyaml.Unmarshal(marshalledManifest, &manifestInterfaceMap)
	if err != nil {
		return nil, err
	}

	duplicateValues := map[string]duplicateYamlValue{}
	duplicateValues = markDuplicateValues(reflect.ValueOf(manifestInterfaceMap), duplicateValues)

	marshalledManifest, err = goyaml.Marshal(&manifestInterfaceMap)
	if err != nil {
		return nil, err
	}

	// Remove quotes over anchor values as reflect in go adds quotes to strings.
	for _, v := range duplicateValues {
		marshalledManifest = bytes.ReplaceAll(marshalledManifest,
			[]byte(fmt.Sprintf("'*%s'", v.Hash)), []byte("*"+v.Hash))
		marshalledManifest = bytes.ReplaceAll(marshalledManifest,
			[]byte(fmt.Sprintf("%s=%s: ", v.YamlKeyMarker, v.Hash)), []byte(fmt.Sprintf("%s: &%s ", v.YamlKeyMarker, v.Hash)))
	}

	return marshalledManifest, nil
}

// markDuplicateValues will store the duplicate values in the
// duplicateValues struct and change the manifest to include anchors.
// Ex :-  key1=UUID1: |-
//		  		data
//		  key2: *UUID1
// Later in the marshal function, the above gets changed to
// Ex :-  key1: &UUID |-
//		  		data
//		  key2: *UUID1
//
func markDuplicateValues(value reflect.Value, duplicateValues map[string]duplicateYamlValue) map[string]duplicateYamlValue {
	// Get the element if the value is a pointer
	if value.Kind() == reflect.Ptr || value.Kind() == reflect.Interface {
		value = value.Elem()
	}

	switch value.Kind() {

	case reflect.Array, reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			duplicateValues = markDuplicateValues(value.Index(i), duplicateValues)
		}
	case reflect.Struct:
		valueKeyField := value.Field(0)
		valueField := value.Field(1)

		valueFieldO := valueField
		if valueField.Kind() == reflect.Ptr || valueField.Kind() == reflect.Interface {
			valueField = valueField.Elem()
		}
		if valueField.Kind() == reflect.String {
			if valueField.String() != "" && valueField.IsValid() && len(valueField.String()) > 64 {
				h := crypto.SHA1.New()
				_, _ = h.Write([]byte(valueField.String()))
				sum := h.Sum(nil)
				sha1 := hex.EncodeToString(sum[:])

				_, foundValue := duplicateValues[sha1]
				if foundValue {
					valueFieldO.Set(reflect.ValueOf("*" + sha1))
				} else {
					newMapKey := fmt.Sprintf("%s=%s", valueKeyField.Interface().(string), sha1)
					valueFieldO.Set(valueField)

					duplicateValue := duplicateYamlValue{
						Hash:          sha1,
						YamlKeyMarker: valueKeyField.Interface().(string),
					}
					valueKeyField.Set(reflect.ValueOf(newMapKey))

					duplicateValues[sha1] = duplicateValue
				}
			}
		} else {
			duplicateValues = markDuplicateValues(valueField, duplicateValues)
		}

	case reflect.Map:
		for _, k := range value.MapKeys() {
			valueField := value.MapIndex(k)
			if valueField.Kind() == reflect.Ptr || valueField.Kind() == reflect.Interface {
				valueField = valueField.Elem()
			}

			// Consider the strings which are big enough only.
			if valueField.Kind() == reflect.String {
				if valueField.String() != "" && valueField.IsValid() {
					h := crypto.SHA1.New()
					_, _ = h.Write([]byte(valueField.String()))
					sum := h.Sum(nil)
					sha1 := hex.EncodeToString(sum[:])

					_, foundValue := duplicateValues[sha1]
					if foundValue {
						value.SetMapIndex(k, reflect.ValueOf(string("*"+sha1)))
					} else {
						newMapKey := fmt.Sprintf("%s=%s", k.Interface().(string), sha1)

						value.SetMapIndex(k, reflect.Value{})
						value.SetMapIndex(reflect.ValueOf(newMapKey), valueField)
						duplicateValue := duplicateYamlValue{
							Hash:          sha1,
							YamlKeyMarker: k.Interface().(string),
						}
						duplicateValues[sha1] = duplicateValue
					}
				}
			} else {
				duplicateValues = markDuplicateValues(value.MapIndex(k), duplicateValues)
			}
		}
	}
	return duplicateValues
}

// SHA1 calculates the SHA1 of the manifest
func (m *Manifest) SHA1() (string, error) {
	manifestBytes, err := m.Marshal()
	if err != nil {
		return "", errors.Wrapf(err, "YAML marshalling manifest failed.")
	}

	return fmt.Sprintf("%x", sha1.Sum(manifestBytes)), nil
}

// GetReleaseImage returns the release image location for a given instance group/job
func (m *Manifest) GetReleaseImage(instanceGroupName, jobName string) (string, error) {
	var instanceGroup *InstanceGroup
	for i := range m.InstanceGroups {
		if m.InstanceGroups[i].Name == instanceGroupName {
			instanceGroup = m.InstanceGroups[i]
			break
		}
	}
	if instanceGroup == nil {
		return "", errors.Errorf("instance group '%s' not found.", instanceGroupName)
	}

	var stemcell *Stemcell
	for i := range m.Stemcells {
		if m.Stemcells[i].Alias == instanceGroup.Stemcell {
			stemcell = m.Stemcells[i]
		}
	}

	var job *Job
	for i := range instanceGroup.Jobs {
		if instanceGroup.Jobs[i].Name == jobName {
			job = &instanceGroup.Jobs[i]
			break
		}
	}
	if job == nil {
		return "", errors.Errorf("job '%s' not found in instance group '%s'", jobName, instanceGroupName)
	}

	for i := range m.Releases {
		if m.Releases[i].Name == job.Release {
			release := m.Releases[i]
			name := strings.TrimRight(release.URL, "/")

			var stemcellVersion string

			if release.Stemcell != nil {
				stemcellVersion = release.Stemcell.OS + "-" + release.Stemcell.Version
			} else {
				if stemcell == nil {
					return "", errors.Errorf("stemcell could not be resolved for instance group %s", instanceGroup.Name)
				}
				stemcellVersion = stemcell.OS + "-" + stemcell.Version
			}
			return fmt.Sprintf("%s/%s:%s-%s", name, release.Name, stemcellVersion, release.Version), nil
		}
	}
	return "", errors.Errorf("release '%s' not found", job.Release)
}

// AddReleasesLabels modifies the manifest by adding to each instance group
// a label for each release used by its jobs
func (m *Manifest) AddReleasesLabels() error {
	var instanceGroup *InstanceGroup
	for i := range m.InstanceGroups {
		instanceGroup = m.InstanceGroups[i]

		var stemcell *Stemcell
		for j := range m.Stemcells {
			if m.Stemcells[j].Alias == instanceGroup.Stemcell {
				stemcell = m.Stemcells[j]
			}
		}

		for _, job := range instanceGroup.Jobs {
			for _, release := range m.Releases {
				if release.Name == job.Release {
					var stemcellVersion string
					if release.Stemcell != nil {
						stemcellVersion = release.Stemcell.OS + "-" + release.Stemcell.Version
					} else {
						if stemcell == nil {
							return errors.Errorf("stemcell could not be resolved for instance group %s", instanceGroup.Name)
						}
						stemcellVersion = stemcell.OS + "-" + stemcell.Version
					}

					labelName := fmt.Sprintf("app.kubernetes.io/version-image-%s", job.Release)
					labelValue := fmt.Sprintf("%s-%s", stemcellVersion, release.Version)
					if instanceGroup.Env.AgentEnvBoshConfig.Agent.Settings.Labels == nil {
						instanceGroup.Env.AgentEnvBoshConfig.Agent.Settings.Labels = make(map[string]string)
					}
					instanceGroup.Env.AgentEnvBoshConfig.Agent.Settings.Labels[labelName] = labelValue
				}
			}
		}
	}

	return nil
}

// GetJobOS returns the stemcell layer OS used for a Job
// This is used for matching addon placement rules
func (m *Manifest) GetJobOS(instanceGroupName, jobName string) (string, error) {
	var instanceGroup *InstanceGroup
	for i := range m.InstanceGroups {
		if m.InstanceGroups[i].Name == instanceGroupName {
			instanceGroup = m.InstanceGroups[i]
			break
		}
	}
	if instanceGroup == nil {
		return "", fmt.Errorf("instance group '%s' not found", instanceGroupName)
	}

	var stemcell *Stemcell
	for i := range m.Stemcells {
		if m.Stemcells[i].Alias == instanceGroup.Stemcell {
			stemcell = m.Stemcells[i]
		}
	}

	var job *Job
	for i := range instanceGroup.Jobs {
		if instanceGroup.Jobs[i].Name == jobName {
			job = &instanceGroup.Jobs[i]
			break
		}
	}
	if job == nil {
		return "", fmt.Errorf("job '%s' not found in instance group '%s'", jobName, instanceGroupName)
	}

	for i := range m.Releases {
		if m.Releases[i].Name == job.Release {
			release := m.Releases[i]

			var stemcellOS string

			if release.Stemcell != nil {
				stemcellOS = release.Stemcell.OS
			} else {
				if stemcell == nil {
					return "", fmt.Errorf("stemcell OS could not be resolved for instance group %s", instanceGroup.Name)
				}
				stemcellOS = stemcell.OS
			}
			return stemcellOS, nil
		}
	}
	return "", fmt.Errorf("release '%s' not found", job.Release)
}

// SlashedVariable returns true if the variable name contains a slash.
// This could be a https://bosh.io/docs/cli-int/#absolute explicit variable,
// but more likely it's the '/' syntax that was introduced to specify the key
// in a secret, e.g. 'ca.private_key' for *implicit variables*.
// Explicit variables use a dot to separate the secret name from the key.
func SlashedVariable(name string) bool {
	return strings.Contains(name, "/")
}

// ImplicitVariables returns a list of all implicit variables in a manifest
func (m *Manifest) ImplicitVariables() ([]string, error) {
	varMap := make(map[string]bool)

	manifestBytes, err := m.Marshal()
	if err != nil {
		return nil, err
	}

	rawManifest := string(manifestBytes)

	// Collect all variables
	varRegexp := regexp.MustCompile(`\(\((!?[-/\.\w\pL]+)\)\)`)
	fieldRegexp := regexp.MustCompile(`[^\.]+`)
	for _, match := range varRegexp.FindAllStringSubmatch(rawManifest, -1) {
		main := match[1]

		// variables with a slash are passed through
		if !SlashedVariable(main) {
			// This stores only the name part of a dotted explicit variable.
			// Remove subfields from explicit vars, e.g. ca.private_key -> ca
			main = fieldRegexp.FindString(match[1])
		}

		// store the name of the potentially implicit variable
		varMap[main] = true
	}

	// Remove the explicit ones
	for _, v := range m.Variables {
		varMap[v.Name] = false
	}

	names := []string{}
	for k, v := range varMap {
		if v {
			names = append(names, k)
		}
	}

	// Slashed implicit vars are returned multiple times, e.g. ca/cert, ca/key
	return names, nil
}

// ApplyAddons goes through all defined addons and adds jobs to matched instance groups
func (m *Manifest) ApplyAddons(log *zap.SugaredLogger) error {
	if m.AddOnsApplied {
		return nil
	}
	for _, addon := range m.AddOns {
		if addon.Name == BoshDNSAddOnName {
			continue
		}
		for _, ig := range m.InstanceGroups {
			include, err := m.addOnPlacementMatch(log, "inclusion", ig, addon.Include)
			if err != nil {
				return errors.Wrap(err, "failed to process include placement matches")
			}
			exclude, err := m.addOnPlacementMatch(log, "exclusion", ig, addon.Exclude)
			if err != nil {
				return errors.Wrap(err, "failed to process exclude placement matches")
			}

			if exclude || !include {
				log.Debugf("Addon '%s' doesn't match instance group '%s'", addon.Name, ig.Name)
				continue
			}

			for _, addonJob := range addon.Jobs {
				addedJob := Job{
					Name:       addonJob.Name,
					Release:    addonJob.Release,
					Properties: addonJob.Properties,
					Consumes:   addonJob.Consumes,
					Provides:   addonJob.Provides,
				}

				addedJob.Properties.Quarks.IsAddon = true

				log.Debugf("Applying addon job '%s/%s' to instance group '%s'", addon.Name, addonJob.Name, ig.Name)
				ig.Jobs = append(ig.Jobs, addedJob)
			}
		}
	}

	// Remember that addons are already applied, so we don't end up applying them again
	m.AddOnsApplied = true

	return nil
}

// PropagateGlobalUpdateBlockToIGs copies the update block to all instance groups
func (m *Manifest) PropagateGlobalUpdateBlockToIGs() {
	for _, ig := range m.InstanceGroups {
		if ig.Update == nil {
			ig.Update = m.Update
		} else {
			if ig.Update.CanaryWatchTime == "" {
				ig.Update.CanaryWatchTime = m.Update.CanaryWatchTime
			}
			if ig.Update.UpdateWatchTime == "" {
				ig.Update.UpdateWatchTime = m.Update.UpdateWatchTime
			}
			if ig.Update.Serial == nil {
				ig.Update.Serial = m.Update.Serial
			}
		}
	}
}

// ListMissingProviders returns a list of missing providers from the manifest
func (m *Manifest) ListMissingProviders() map[string]bool {
	provideAsNames := map[string]bool{}
	consumeFromNames := map[string]bool{}

	for _, ig := range m.InstanceGroups {
		for _, job := range ig.Jobs {
			provideAsNames = listProviderNames(provideAsNames, job.Provides, "as")
			consumeFromNames = listProviderNames(consumeFromNames, job.Consumes, "from")
		}
	}

	// Iterate consumeFromNames and remove providers existing in the manifest
	for providerName := range consumeFromNames {
		if _, ok := provideAsNames[providerName]; ok {
			delete(consumeFromNames, providerName)
		}
	}

	return consumeFromNames
}

// listProviderNames returns a map containing provider names from job provides and consumes
func listProviderNames(providerNames map[string]bool, providerProperties map[string]interface{}, providerKey string) map[string]bool {
	for _, property := range providerProperties {
		p, ok := property.(map[string]interface{})
		if !ok {
			continue
		}
		nameVal, ok := p[providerKey]
		if !ok {
			continue
		}

		name, _ := nameVal.(string)
		if len(name) == 0 {
			continue
		}
		providerNames[name] = false
	}

	return providerNames
}
