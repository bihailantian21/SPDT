package policies_derivation

import (
	"github.com/Cloud-Pie/SPDT/types"
	"github.com/Cloud-Pie/SPDT/util"
	"time"
	"github.com/Cloud-Pie/SPDT/rest_clients/scheduler"
	"math"
	"sort"
	"github.com/Cloud-Pie/SPDT/rest_clients/performance_profiles"
	"strconv"
	"github.com/op/go-logging"
	"github.com/Cloud-Pie/SPDT/storage"
	"github.com/Cloud-Pie/SPDT/config"
	"errors"
)

var log = logging.MustGetLogger("spdt")

//Interface for strategies of how to scale
type PolicyDerivation interface {
	CreatePolicies (processedForecast types.ProcessedForecast) []types.Policy
	FindSuitableVMs (numberReplicas int, limits types.Limit) types.VMScale
}

//Interface for strategies of when to scale
type TimeWindowDerivation interface {
	NumberIntervals()	int
	WindowDerivation(values []float64, times [] time.Time)	types.ProcessedForecast
}

/* Derive scaling policies
	in:
		@poiList []types.PoI
		@values []float64
		@times [] time.Time
		@sortedVMProfiles []VmProfile
		@sysConfiguration SystemConfiguration
	out:
		@[]types.Policy
*/
func Policies(poiList []types.PoI, values []float64, times [] time.Time, sortedVMProfiles []types.VmProfile, sysConfiguration config.SystemConfiguration) []types.Policy {
	var policies []types.Policy

	currentState,err := scheduler.CurrentState(sysConfiguration.SchedulerComponent.Endpoint + util.ENDPOINT_CURRENT_STATE)
	if err != nil {
		log.Error("Error to get current state")
	}

	timeWindows := SmallStepOverProvision{PoIList:poiList}
	processedForecast := timeWindows.WindowDerivation(values,times)

	mapVMProfiles := VMListToMap(sortedVMProfiles)

	switch sysConfiguration.PreferredAlgorithm {
	case util.NAIVE_ALGORITHM:
		naive := NaivePolicy {algorithm:util.NAIVE_ALGORITHM, timeWindow:timeWindows,
							 currentState:currentState, mapVMProfiles:mapVMProfiles, sysConfiguration: sysConfiguration}
		policies = naive.CreatePolicies(processedForecast)

	case util.BASE_INSTANCE_ALGORITHM:
		base := BestBaseInstancePolicy{algorithm:util.BASE_INSTANCE_ALGORITHM, timeWindow:timeWindows,
										currentState:currentState,mapVMProfiles:mapVMProfiles, sysConfiguration: sysConfiguration}
		policies = base.CreatePolicies(processedForecast)

	case util.SMALL_STEP_ALGORITHM:
		sstep := StepRepackPolicy{algorithm:util.SMALL_STEP_ALGORITHM, timeWindow:timeWindows,
									mapVMProfiles:mapVMProfiles ,sysConfiguration: sysConfiguration, currentState:currentState}
		policies = sstep.CreatePolicies(processedForecast)

	case util.SEARCH_TREE_ALGORITHM:
		tree := TreePolicy {algorithm:util.SEARCH_TREE_ALGORITHM, timeWindow:timeWindows, currentState:currentState,
			sortedVMProfiles:sortedVMProfiles,mapVMProfiles:mapVMProfiles,sysConfiguration: sysConfiguration}
		policies = tree.CreatePolicies(processedForecast)

	case util.DELTA_REPACKED:
		algorithm := DeltaRepackedPolicy {algorithm:util.DELTA_REPACKED, timeWindow:timeWindows, currentState:currentState,
		sortedVMProfiles:sortedVMProfiles, mapVMProfiles:mapVMProfiles, sysConfiguration: sysConfiguration}
		policies = algorithm.CreatePolicies(processedForecast)
	default:
		//naive
		naive := NaivePolicy {algorithm:util.NAIVE_ALGORITHM, timeWindow:timeWindows,
			currentState:currentState, mapVMProfiles:mapVMProfiles, sysConfiguration: sysConfiguration}
		policies1 := naive.CreatePolicies(processedForecast)
		policies = append(policies, policies1...)
		//types
		base := BestBaseInstancePolicy{algorithm:util.BASE_INSTANCE_ALGORITHM, timeWindow:timeWindows,
			currentState:currentState,mapVMProfiles:mapVMProfiles, sysConfiguration: sysConfiguration}
		policies2 := base.CreatePolicies(processedForecast)
		policies = append(policies, policies2...)
		//sstep
		sstep := StepRepackPolicy{algorithm:util.SMALL_STEP_ALGORITHM, timeWindow:timeWindows,
			mapVMProfiles:mapVMProfiles ,sysConfiguration: sysConfiguration, currentState:currentState}
		policies3 := sstep.CreatePolicies(processedForecast)
		policies = append(policies, policies3...)
		//delta repack
		algorithm := DeltaRepackedPolicy {algorithm:util.DELTA_REPACKED, timeWindow:timeWindows, currentState:currentState,
			sortedVMProfiles:sortedVMProfiles, mapVMProfiles:mapVMProfiles, sysConfiguration: sysConfiguration}
		policies4 := algorithm.CreatePolicies(processedForecast)
		policies = append(policies, policies4...)

		//tree
		tree := TreePolicy {algorithm:util.SEARCH_TREE_ALGORITHM, timeWindow:timeWindows, currentState:currentState,
			sortedVMProfiles:sortedVMProfiles,mapVMProfiles:mapVMProfiles,sysConfiguration: sysConfiguration}
		policies5 := tree.CreatePolicies(processedForecast)
		policies = append(policies, policies5...)
	}
	return policies
}

/* Compute the booting time that will take a set of VMS
	in:
		@vmsScale types.VMScale
		@sysConfiguration SystemConfiguration
	out:
		@int	Time in seconds that the booting wil take
*/
func computeVMBootingTime(vmsScale types.VMScale, sysConfiguration config.SystemConfiguration) int {
	bootTime := 0
	// If Heterogeneous cluster, take the bigger cluster
	list := mapToList(vmsScale)
	sort.Slice(list, func(i, j int) bool {
		return list[i].Value > list[j].Value
	})

	//Check in db if already data is stored
	//Call API
	if len(list) > 0 {
		url := sysConfiguration.PerformanceProfilesComponent.Endpoint + util.ENDPOINT_VM_TIMES
		times, error := performance_profiles.GetBootShutDownProfile(url,list[0].Key, list[0].Value)
		if error != nil {
			log.Error("Error in bootingTime query", error.Error())
		}
		bootTime = times.BootTime
	}
	return bootTime
}


/* Compute the termination time of a set of VMs
	in:
		@vmsScale types.VMScale
		@sysConfiguration SystemConfiguration
	out:
		@int	Time in seconds that the termination wil take
*/
func computeVMTerminationTime(vmsScale types.VMScale, sysConfiguration config.SystemConfiguration) int {
	terminationTime := 0
	list := mapToList(vmsScale)
	sort.Slice(list, func(i, j int) bool {
		return list[i].Value > list[j].Value
	})

	//Check in db if already data is stored
	//Call API
	if len(list) > 0 {
		url := sysConfiguration.PerformanceProfilesComponent.Endpoint + util.ENDPOINT_VM_TIMES
		times, error := performance_profiles.GetBootShutDownProfile(url,list[0].Key, list[0].Value)
		if error != nil {
			log.Error("Error in terminationTime query %s", error.Error())
		}
		terminationTime = times.ShutDownTime
	}
	return terminationTime
}

/* Compute the max number of service replicas (Replicas capacity) that a VM can host
	in:
		@vmProfile types.VmProfile
		@resourceLimit types.Limit
	out:
		@int	Number of replicas
*/
func maxReplicasCapacityInVM(vmProfile types.VmProfile, resourceLimit types.Limit) int {
		m := float64(vmProfile.NumCores) / float64(resourceLimit.NumberCores)
		n := float64(vmProfile.Memory) / float64(resourceLimit.MemoryGB)
		numReplicas := math.Min(n,m)
		return int(numReplicas)
}

/* Select the service profile for a given container limit resources
	in:
		@requests	float64 - number of requests that the service should serve
		@limits types.Limit	- resource limits (cpu cores and memory gb) configured in the container
		@underProvision bool	- flag that indicate if when searching for a service profile, the underprovision is allowed
	out:
		@ContainersConfig	- configuration with number of replicas and limits that best fit for the number of requests
*/
func selectProfileWithLimits(requests float64, limits types.Limit, underProvision bool) types.ContainersConfig {
	var containerConfig types.ContainersConfig
	serviceProfileDAO := storage.GetPerformanceProfileDAO()
	overProvisionConfig, err1 := serviceProfileDAO.MatchByLimitsOver(limits.NumberCores, limits.MemoryGB, requests)
	underProvisionConfig, err2 := serviceProfileDAO.MatchByLimitsUnder(limits.NumberCores, limits.MemoryGB, requests)

	if underProvision && err2 == nil {
		containerConfig = underProvisionConfig
	} else if err1 == nil{
		containerConfig = overProvisionConfig
	} else if err2 == nil {
		containerConfig = underProvisionConfig
	}

	return containerConfig
}

/* Select the service profile for any limit resources that satisfies the number of requests
	in:
		@requests	float64 - number of requests that the service should serve
		@underProvision bool	- flag that indicate if when searching for a service profile, the underprovision is allowed
	out:
		@ContainersConfig	- configuration with number of replicas and limits that best fit for the number of requests
*/
func selectProfile(requests float64, underProvision bool) types.ContainersConfig {

	var profiles []types.ContainersConfig
	serviceProfileDAO := storage.GetPerformanceProfileDAO()
	profilesUnder,err1:= serviceProfileDAO.MatchUnder(requests)
	profilesOver,err2 := serviceProfileDAO.MatchOver(requests)

	if underProvision && err2 == nil && len(profilesUnder)>0 {
		profiles = profilesUnder
	} else if err1 == nil{
		profiles = profilesOver
	} else if err2 == nil {
		profiles = profilesUnder
	}

	sort.Slice(profiles, func(i, j int) bool {
		utilizationFactori := float64(profiles[i].PerformanceProfile.NumberReplicas) * profiles[i].Limits.NumberCores +  float64(profiles[i].PerformanceProfile.NumberReplicas) * profiles[i].Limits.MemoryGB
		utilizationFactorj := float64(profiles[j].PerformanceProfile.NumberReplicas) * profiles[j].Limits.NumberCores + float64(profiles[j].PerformanceProfile.NumberReplicas) * profiles[i].Limits.MemoryGB
		return utilizationFactori < utilizationFactorj
	})

	return profiles[0]
}

/* Select the service profile for any limit resources that satisfies the number of requests
	in:
		@numberReplicas	int - number of replicas
		@limits bool types.Limit - limits constraints(cpu cores and memory gb) per replica
	out:
		@float64	- Max number of request for this containers configuration
*/
func configurationLoadCapacity(numberReplicas int, limits types.Limit) float64 {
	serviceProfileDAO := storage.GetPerformanceProfileDAO()
	profile,_ := serviceProfileDAO.FindProfileTRN(limits.NumberCores, limits.MemoryGB, numberReplicas)
	currentLoadCapacity := profile.TRNConfiguration[0].TRN

	return currentLoadCapacity
}

/* Utility method to set up each scaling configuration
*/
func setConfiguration(configurations *[]types.ScalingConfiguration, state types.State, timeStart time.Time, timeEnd time.Time, name string, totalServicesBootingTime int, sysConfiguration config.SystemConfiguration, stateLoadCapacity float64) {
	nConfigurations := len(*configurations)
	if nConfigurations >= 1 && state.Equal((*configurations)[nConfigurations-1].State) {
		(*configurations)[nConfigurations-1].TimeEnd = timeEnd
	} else {
		//var deltaTime int //time in seconds
		var finishTimeVMRemoved int
		var bootTimeVMAdded int

		//Adjust booting times for resources configuration
		if nConfigurations >= 1 {
			vmAdded, vmRemoved := deltaVMSet((*configurations)[nConfigurations-1].State.VMs ,state.VMs)
			//Adjust previous configuration
			if len(vmRemoved) > 0 {
				finishTimeVMRemoved = computeVMTerminationTime(vmRemoved, sysConfiguration)
				previousTimeEnd := (*configurations)[nConfigurations-1].TimeEnd
				(*configurations)[nConfigurations-1].TimeEnd = previousTimeEnd.Add(time.Duration(finishTimeVMRemoved) * time.Second)
			}
			if len(vmAdded) > 0 {
				bootTimeVMAdded = computeVMBootingTime(vmAdded, sysConfiguration)
			}
		}
		startTime := timeStart.Add(-1 * time.Duration(bootTimeVMAdded) * time.Second)       //Booting/Termination time VM
		startTime = startTime.Add(-1 * time.Duration(totalServicesBootingTime) * time.Second) //Start time containers
		state.LaunchTime = startTime
		state.Name = strconv.Itoa(nConfigurations) + "__" + name + "__" + startTime.Format(util.TIME_LAYOUT)
		*configurations = append(*configurations,
			types.ScalingConfiguration{
				State:          state,
				TimeStart:      timeStart,
				TimeEnd:        timeEnd,
				Metrics:types.ConfigMetrics{CapacityTRN:stateLoadCapacity,},
			})
	}
}

/* Build Heterogeneous cluster to deploy a number of replicas, each one with the defined constraint limits
	in:
		@numberReplicas	int - number of replicas
		@limits bool types.Limit - limits constraints(cpu cores and memory gb) per replica
		@mapVMProfiles - map with the profiles of VMs available
	out:
		@VMScale	- Map with the type of VM as key and the number of vms as value
*/
func buildHeterogeneousVMSet(numberReplicas int, limits types.Limit, mapVMProfiles map[string]types.VmProfile) (types.VMScale,error) {
	var err error
	tree := &Tree{}
	node := new(Node)
	node.NReplicas = numberReplicas
	node.vmScale = make(map[string]int)
	tree.Root = node
	candidateVMSets := []types.VMScale {}
	computeVMsCapacity(limits, &mapVMProfiles)

	buildTree(tree.Root, numberReplicas,&candidateVMSets,mapVMProfiles)

	if len(candidateVMSets)> 0{
		sort.Slice(candidateVMSets, func(i, j int) bool {
			costi := candidateVMSets[i].Cost(mapVMProfiles)
			costj := candidateVMSets[j].Cost(mapVMProfiles)
			if costi < costj {
				return true
			} else if costi ==  costj {
				return candidateVMSets[i].TotalVMs() >= candidateVMSets[j].TotalVMs()
			}
			return false
		})
		return candidateVMSets[0],err
	}else {
		return types.VMScale{},errors.New("No VM Candidate")
	}
}

/*
	Form scaling options using clusters of heterogeneous VMs
	Builds a tree to form the different combinations
	in:
		@node			- Node of the tree
		@numberReplicas	- Number of replicas that the VM set should host
		@vmScaleList	- List filled with the candidates VM sets
*/
func buildTree(node *Node, numberReplicas int, vmScaleList *[]types.VMScale, mapVMProfiles map[string]types.VmProfile) *Node {
	if node.NReplicas == 0 {
		return node
	}
	for k,v := range mapVMProfiles {
		maxReplicas := v.ReplicasCapacity
		if maxReplicas >= numberReplicas {
			newNode := new(Node)
			newNode.vmType = k
			newNode.NReplicas = 0
			newNode.vmScale = copyMap(node.vmScale)
			if _, ok := newNode.vmScale[newNode.vmType]; ok {
				newNode.vmScale[newNode.vmType] = newNode.vmScale[newNode.vmType]+1
			} else {
				newNode.vmScale[newNode.vmType] = 1
			}
			node.children = append(node.children, newNode)
			*vmScaleList = append(*vmScaleList, newNode.vmScale)
			//return node
		} else if maxReplicas > 0 {
			newNode := new(Node)
			newNode.vmType = k
			newNode.NReplicas = numberReplicas -maxReplicas
			newNode.vmScale = copyMap(node.vmScale)
			if _, ok := newNode.vmScale[newNode.vmType]; ok {
				newNode.vmScale[newNode.vmType] = newNode.vmScale[newNode.vmType] + 1
			} else {
				newNode.vmScale[newNode.vmType] = 1
			}
			newNode = buildTree(newNode, numberReplicas-maxReplicas, vmScaleList, mapVMProfiles)
			node.children = append(node.children, newNode)
		}
	}
	return node
}

/* Build Homogeneous cluster to deploy a number of replicas, each one with the defined constraint limits
	in:
		@numberReplicas	int - number of replicas
		@limits bool types.Limit - limits constraints(cpu cores and memory gb) per replica
		@mapVMProfiles - map with the profiles of VMs available
	out:
		@VMScale	- Map with the type of VM as key and the number of vms as value
*/
func buildHomogeneousVMSet(numberReplicas int, limits types.Limit, mapVMProfiles map[string]types.VmProfile) (types.VMScale,error) {
	var err error
	candidateVMSets := []types.VMScale{}
	for _,v := range mapVMProfiles {
		vmScale :=  make(map[string]int)
		replicasCapacity :=  maxReplicasCapacityInVM(v, limits)
		if replicasCapacity > 0 {
			numVMs := math.Ceil(float64(numberReplicas) / float64(replicasCapacity))
			vmScale[v.Type] = int(numVMs)
			candidateVMSets = append(candidateVMSets, vmScale)
		}
	}
	if len(candidateVMSets) > 0 {
		sort.Slice(candidateVMSets, func(i, j int) bool {
			costi := candidateVMSets[i].Cost(mapVMProfiles)
			costj := candidateVMSets[j].Cost(mapVMProfiles)
			if costi < costj {
				return true
			} else if costi ==  costj {
				return candidateVMSets[i].TotalVMs() >= candidateVMSets[j].TotalVMs()
			}
			return false
		})
		return candidateVMSets[0],err
	}else {
		return types.VMScale{},errors.New("No VM Candidate")
	}
}