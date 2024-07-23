package ovirt

import (
	"fmt"
	"strings"

	"github.com/bl4ko/netbox-ssot/internal/constants"
	"github.com/bl4ko/netbox-ssot/internal/devices"
	"github.com/bl4ko/netbox-ssot/internal/netbox/inventory"
	"github.com/bl4ko/netbox-ssot/internal/netbox/objects"
	"github.com/bl4ko/netbox-ssot/internal/source/common"
	"github.com/bl4ko/netbox-ssot/internal/utils"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

// Syncs networks received from oVirt API to the netbox.
func (o *OVirtSource) syncNetworks(nbi *inventory.NetboxInventory) error {
	for _, network := range o.Networks.OVirtNetworks {
		name, exists := network.Name()
		if !exists {
			return fmt.Errorf("network %v has no name", network)
		}
		description, _ := network.Description()
		// TODO: handle other networks
		if networkVlan, exists := network.Vlan(); exists {
			// Get vlanGroup from relation
			vlanGroup, err := common.MatchVlanToGroup(o.Ctx, nbi, name, o.VlanGroupRelations)
			if err != nil {
				return err
			}
			// Get tenant from relation
			vlanTenant, err := common.MatchVlanToTenant(o.Ctx, nbi, name, o.VlanTenantRelations)
			if err != nil {
				return err
			}
			if networkVlanID, exists := networkVlan.Id(); exists {
				_, err := nbi.AddVlan(o.Ctx, &objects.Vlan{
					NetboxObject: objects.NetboxObject{
						Description: description,
						Tags:        o.Config.SourceTags,
						CustomFields: map[string]interface{}{
							constants.CustomFieldSourceName: o.SourceConfig.Name,
						},
					},
					Name:     name,
					Group:    vlanGroup,
					Vid:      int(networkVlanID),
					Status:   &objects.VlanStatusActive,
					Tenant:   vlanTenant,
					Comments: network.MustComment(),
				})
				if err != nil {
					return fmt.Errorf("adding vlan: %v", err)
				}
			}
		}
	}
	return nil
}

func (o *OVirtSource) syncDatacenters(nbi *inventory.NetboxInventory) error {
	for _, datacenter := range o.DataCenters {
		dcName, exists := datacenter.Name()
		if !exists {
			return fmt.Errorf("failed to get name for oVirt datacenter %s", dcName)
		}
		description, _ := datacenter.Description()
		nbClusterGroupName := dcName
		if mappedClusterGroupName, ok := o.DatacenterClusterGroupRelations[dcName]; ok {
			nbClusterGroupName = mappedClusterGroupName
			o.Logger.Debugf(o.Ctx, "mapping datacenter name %s to cluster group name %s", dcName, mappedClusterGroupName)
		}
		nbClusterGroup := &objects.ClusterGroup{
			NetboxObject: objects.NetboxObject{
				Description: description,
				Tags:        o.Config.SourceTags,
				CustomFields: map[string]interface{}{
					constants.CustomFieldSourceName: o.SourceConfig.Name,
				},
			},
			Name: nbClusterGroupName,
			Slug: utils.Slugify(nbClusterGroupName),
		}
		_, err := nbi.AddClusterGroup(o.Ctx, nbClusterGroup)
		if err != nil {
			return fmt.Errorf("failed to add oVirt data center %s as Netbox cluster group: %v", dcName, err)
		}
	}
	return nil
}

func (o *OVirtSource) syncClusters(nbi *inventory.NetboxInventory) error {
	clusterType := &objects.ClusterType{
		NetboxObject: objects.NetboxObject{
			Tags: o.Config.SourceTags,
			CustomFields: map[string]interface{}{
				constants.CustomFieldSourceName: o.SourceConfig.Name,
			},
		},
		Name: "oVirt",
		Slug: "ovirt",
	}
	clusterType, err := nbi.AddClusterType(o.Ctx, clusterType)
	if err != nil {
		return fmt.Errorf("failed to add oVirt cluster type: %v", err)
	}
	// Then sync oVirt Clusters as NetBoxClusters
	for _, cluster := range o.Clusters {
		clusterName, exists := cluster.Name()
		if !exists {
			return fmt.Errorf("failed to get name for oVirt cluster %s", clusterName)
		}
		description, exists := cluster.Description()
		if !exists {
			o.Logger.Warning(o.Ctx, "description for oVirt cluster ", clusterName, " is empty.")
		}
		var clusterGroup *objects.ClusterGroup
		var clusterGroupName string
		if _, ok := o.DataCenters[cluster.MustDataCenter().MustId()]; ok {
			clusterGroupName = o.DataCenters[cluster.MustDataCenter().MustId()].MustName()
		} else {
			o.Logger.Warning(o.Ctx, "failed to get datacenter for oVirt cluster ", clusterName)
		}
		if clusterGroupName != "" {
			if mappedName, ok := o.DatacenterClusterGroupRelations[clusterGroupName]; ok {
				clusterGroupName = mappedName
			}
			clusterGroup = nbi.ClusterGroupsIndexByName[clusterGroupName]
		}

		clusterSite, err := common.MatchClusterToSite(o.Ctx, nbi, clusterName, o.ClusterSiteRelations)
		if err != nil {
			return fmt.Errorf("match cluster to site: %s", err)
		}

		clusterTenant, err := common.MatchClusterToTenant(o.Ctx, nbi, clusterName, o.ClusterTenantRelations)
		if err != nil {
			return fmt.Errorf("match cluster to tenant: %s", err)
		}

		nbCluster := &objects.Cluster{
			NetboxObject: objects.NetboxObject{
				Description: description,
				Tags:        o.Config.SourceTags,
				CustomFields: map[string]interface{}{
					constants.CustomFieldSourceName: o.SourceConfig.Name,
				},
			},
			Name:   clusterName,
			Type:   clusterType,
			Status: objects.ClusterStatusActive,
			Group:  clusterGroup,
			Site:   clusterSite,
			Tenant: clusterTenant,
		}
		_, err = nbi.AddCluster(o.Ctx, nbCluster)
		if err != nil {
			return fmt.Errorf("failed to add oVirt cluster %s as Netbox cluster: %v", clusterName, err)
		}
	}
	return nil
}

// syncHosts synces collected hosts from ovirt api to netbox inventory
// as devices.
func (o *OVirtSource) syncHosts(nbi *inventory.NetboxInventory) error {
	for hostID, host := range o.Hosts {
		hostName, exists := host.Name()
		if !exists {
			o.Logger.Warningf(o.Ctx, "name of host with id=%s is empty", hostID)
		}
		hostCluster := nbi.ClustersIndexByName[o.Clusters[host.MustCluster().MustId()].MustName()]

		hostSite, err := common.MatchHostToSite(o.Ctx, nbi, hostName, o.HostSiteRelations)
		if err != nil {
			return fmt.Errorf("hostSite: %s", err)
		}
		hostTenant, err := common.MatchHostToTenant(o.Ctx, nbi, hostName, o.HostTenantRelations)
		if err != nil {
			return fmt.Errorf("hostTenant: %s", err)
		}

		// Extract host hardware information if possible, if not use generic values
		var hostSerialNumber, hostUUID string
		hostManufacturerName := constants.DefaultManufacturer
		hostModel := constants.DefaultModel
		if hwInfo, exists := host.HardwareInformation(); exists {
			hostUUID, _ = hwInfo.Uuid()
			hostSerialNumber, _ = hwInfo.SerialNumber()
			if manufacturerName, exists := hwInfo.Manufacturer(); exists {
				hostManufacturerName = manufacturerName
				hostManufacturerName = utils.SerializeManufacturerName(hostManufacturerName)
			}
			if modelName, exists := hwInfo.ProductName(); exists {
				hostModel = modelName
			}
		}

		var deviceSlug string
		deviceData, hasDeviceData := devices.DeviceTypesMap[hostManufacturerName][hostModel]
		if hasDeviceData {
			deviceSlug = deviceData.Slug
		} else {
			deviceSlug = utils.GenerateDeviceTypeSlug(hostManufacturerName, hostModel)
		}

		hostManufacturer, err := nbi.AddManufacturer(o.Ctx, &objects.Manufacturer{
			Name: hostManufacturerName,
			Slug: utils.Slugify(hostManufacturerName),
		})
		if err != nil {
			return fmt.Errorf("failed adding oVirt Manufacturer %v with error: %s", hostManufacturer, err)
		}

		var hostDeviceType *objects.DeviceType
		hostDeviceType, err = nbi.AddDeviceType(o.Ctx, &objects.DeviceType{
			Manufacturer: hostManufacturer,
			Model:        hostModel,
			Slug:         deviceSlug,
		})
		if err != nil {
			return fmt.Errorf("failed adding oVirt DeviceType %v with error: %s", hostDeviceType, err)
		}

		var hostStatus *objects.DeviceStatus
		ovirtStatus, exists := host.Status()
		if exists {
			switch ovirtStatus {
			case ovirtsdk4.HOSTSTATUS_UP:
				hostStatus = &objects.DeviceStatusActive
			default:
				hostStatus = &objects.DeviceStatusOffline
			}
		}

		var hostPlatform *objects.Platform
		var osDistribution, osVersion, osArch string
		if os, exists := host.Os(); exists {
			if ovirtOsType, exists := os.Type(); exists {
				osDistribution = ovirtOsType
			}
			if ovirtOsVersion, exists := os.Version(); exists {
				if osMajorVersion, exists := ovirtOsVersion.Major(); exists {
					osVersion = fmt.Sprintf("%d", osMajorVersion)
				}
			}
			// We extract architecture from reported_kernel_cmdline
			if reportedKernelCmdline, exists := os.ReportedKernelCmdline(); exists {
				osArch = utils.ExtractCPUArch(reportedKernelCmdline)
				if bitArch, ok := constants.Arch2Bit[osArch]; ok {
					osArch = bitArch
				}
			}
		}
		platformName := utils.GeneratePlatformName(osDistribution, osVersion, osArch)
		hostPlatform, err = nbi.AddPlatform(o.Ctx, &objects.Platform{
			Name: platformName,
			Slug: utils.Slugify(platformName),
		})
		if err != nil {
			return fmt.Errorf("failed adding oVirt Platform %v with error: %s", hostPlatform, err)
		}

		var hostDescription string
		if description, exists := host.Description(); exists {
			hostDescription = description
		}

		var hostComment string
		if comment, exists := host.Comment(); exists {
			hostComment = comment
		}

		var hostCPUCores string
		if cpu, exists := host.Cpu(); exists {
			hostCPUCores, exists = cpu.Name()
			if !exists {
				o.Logger.Warning(o.Ctx, "oVirt hostCpuCores of ", hostName, " is empty.")
			}
		}

		mem, _ := host.Memory()
		mem /= (constants.KiB * constants.KiB * constants.KiB) // Value is in Bytes, we convert to GB

		hostDeviceRole, err := nbi.AddDeviceRole(o.Ctx, &objects.DeviceRole{Name: constants.DeviceRoleServer, Slug: utils.Slugify(constants.DeviceRoleServer), Color: constants.DeviceRoleServerColor, VMRole: false})
		if err != nil {
			return err
		}

		nbHost := &objects.Device{
			NetboxObject: objects.NetboxObject{
				Description: hostDescription,
				Tags:        o.Config.SourceTags,
				CustomFields: map[string]interface{}{
					constants.CustomFieldSourceName:       o.SourceConfig.Name,
					constants.CustomFieldSourceIDName:     hostID,
					constants.CustomFieldHostCPUCoresName: hostCPUCores,
					constants.CustomFieldHostMemoryName:   fmt.Sprintf("%d GB", mem),
					constants.CustomFieldDeviceUUIDName:   hostUUID,
				},
			},
			Name:         hostName,
			Status:       hostStatus,
			Platform:     hostPlatform,
			DeviceRole:   hostDeviceRole,
			Site:         hostSite,
			Tenant:       hostTenant,
			Cluster:      hostCluster,
			Comments:     hostComment,
			SerialNumber: hostSerialNumber,
			DeviceType:   hostDeviceType,
		}
		nbHost, err = nbi.AddDevice(o.Ctx, nbHost)
		if err != nil {
			return fmt.Errorf("failed to add oVirt host %s with error: %v", hostName, err)
		}

		// We also need to sync nics separately, because nic is a separate object in netbox
		err = o.syncHostNics(nbi, host, nbHost)
		if err != nil {
			return fmt.Errorf("failed to sync oVirt host %s nics with error: %v", hostName, err)
		}
	}
	return nil
}

func (o *OVirtSource) syncHostNics(nbi *inventory.NetboxInventory, ovirtHost *ovirtsdk4.Host, nbHost *objects.Device) error {
	if nics, exists := ovirtHost.Nics(); exists {
		master2slave := make(map[string][]string) // masterId: [slaveId1, slaveId2, ...]
		parent2child := make(map[string][]string) // parentId: [childId, ... ]
		processedNicsIDs := make(map[string]bool) // set of all nic ids that have already been processed

		nicID2nic := map[string]*objects.Interface{} // nicId: nic
		nicID2IPv4 := map[string]string{}            // nicId: ipAv4address/mask
		nicID2IPv6 := map[string]string{}            // nicId: ipv6Address/mask

		var hostIP string
		if hostAddress, exists := ovirtHost.Address(); exists {
			hostIP = utils.Lookup(hostAddress)
		}

		// First loop, we loop through all the nics and collect all the information
		err := o.collectHostNicsData(nbHost, nbi, nics, parent2child, master2slave, nicID2nic, processedNicsIDs, nicID2IPv4, nicID2IPv6)
		if err != nil {
			return fmt.Errorf("collect host nics data: %s", err)
		}

		// Second loop to add relations between interfaces (e.g. [eno1, eno2] -> bond1)
		for masterID, slavesIDs := range master2slave {
			var err error
			masterInterface := nicID2nic[masterID]
			if _, ok := processedNicsIDs[masterID]; ok {
				masterInterface, err = nbi.AddInterface(o.Ctx, masterInterface)
				if err != nil {
					return fmt.Errorf("failed to add oVirt master interface %s with error: %v", masterInterface.Name, err)
				}
				delete(processedNicsIDs, masterID)
				nicID2nic[masterID] = masterInterface
			}
			for _, slaveID := range slavesIDs {
				slaveInterface := nicID2nic[slaveID]
				slaveInterface.LAG = masterInterface
				slaveInterface, err := nbi.AddInterface(o.Ctx, slaveInterface)
				if err != nil {
					return fmt.Errorf("failed to add oVirt slave interface %s with error: %v", slaveInterface.Name, err)
				}
				delete(processedNicsIDs, slaveID)
				nicID2nic[slaveID] = slaveInterface
			}
		}

		// Third loop we connect children with parents (e.g. [bond1.605, bond1.604, bond1.603] -> bond1)
		for parent, children := range parent2child {
			parentInterface := nicID2nic[parent]
			if _, ok := processedNicsIDs[parent]; ok {
				parentInterface, err := nbi.AddInterface(o.Ctx, parentInterface)
				if err != nil {
					return fmt.Errorf("failed to add oVirt parent interface %s with error: %v", parentInterface.Name, err)
				}
				nicID2nic[parent] = parentInterface
				delete(processedNicsIDs, parent)
			}
			for _, child := range children {
				childInterface := nicID2nic[child]
				childInterface.ParentInterface = parentInterface
				childInterface, err := nbi.AddInterface(o.Ctx, childInterface)
				if err != nil {
					return fmt.Errorf("failed to add oVirt child interface %s with error: %v", childInterface.Name, err)
				}
				nicID2nic[child] = childInterface
				delete(processedNicsIDs, child)
			}
		}

		// Fourth loop we check if there are any nics that were not processed
		for nicID := range processedNicsIDs {
			nbNic, err := nbi.AddInterface(o.Ctx, nicID2nic[nicID])
			if err != nil {
				return fmt.Errorf("failed to add oVirt interface %s with error: %v", nicID2nic[nicID].Name, err)
			}
			nicID2nic[nicID] = nbNic
		}

		// Fifth loop we add ip addresses to interfaces
		for nicID, ipv4 := range nicID2IPv4 {
			nbNic := nicID2nic[nicID]
			address := strings.Split(ipv4, "/")[0]
			if !utils.SubnetsContainIPAddress(address, o.SourceConfig.IgnoredSubnets) {
				nbIPAddress, err := nbi.AddIPAddress(o.Ctx, &objects.IPAddress{
					NetboxObject: objects.NetboxObject{
						Tags: o.Config.SourceTags,
						CustomFields: map[string]interface{}{
							constants.CustomFieldSourceName:   o.SourceConfig.Name,
							constants.CustomFieldArpEntryName: false,
						},
					},
					Address:            ipv4,
					Status:             &objects.IPAddressStatusActive, // TODO
					DNSName:            utils.ReverseLookup(address),
					AssignedObjectType: objects.AssignedObjectTypeDeviceInterface,
					AssignedObjectID:   nbNic.ID,
				})
				if err != nil {
					return fmt.Errorf("add ipv4 address: %s", err)
				}
				if address == hostIP {
					hostCopy := *nbHost
					hostCopy.PrimaryIPv4 = nbIPAddress
					_, err := nbi.AddDevice(o.Ctx, &hostCopy)
					if err != nil {
						return fmt.Errorf("adding primary ipv4 address: %s", err)
					}
				}

				// Also create prefix if it doesn't exist yet
				prefix, mask, err := utils.GetPrefixAndMaskFromIPAddress(nbIPAddress.Address)
				if err != nil {
					o.Logger.Warningf(o.Ctx, "error extracting prefix from IP address: %s", err)
				} else if mask != constants.MaxIPv4MaskBits {
					_, err = nbi.AddPrefix(o.Ctx, &objects.Prefix{
						Prefix: prefix,
					})
					if err != nil {
						o.Logger.Warningf(o.Ctx, "adding prefix: %s", err)
					}
				}
			}
		}
		for nicID, ipv6 := range nicID2IPv6 {
			nbNic := nicID2nic[nicID]
			address := strings.Split(ipv6, "/")[0]
			nbIPAddress, err := nbi.AddIPAddress(o.Ctx, &objects.IPAddress{
				NetboxObject: objects.NetboxObject{
					Tags: o.Config.SourceTags,
					CustomFields: map[string]interface{}{
						constants.CustomFieldSourceName:   o.SourceConfig.Name,
						constants.CustomFieldArpEntryName: false,
					},
				},
				Address:            ipv6,
				Status:             &objects.IPAddressStatusActive, // TODO
				DNSName:            utils.ReverseLookup(address),
				AssignedObjectType: objects.AssignedObjectTypeDeviceInterface,
				AssignedObjectID:   nbNic.ID,
			})
			if err != nil {
				return fmt.Errorf("add ipv6 address: %s", err)
			}

			// Also create prefix if it doesn't exist yet
			prefix, mask, err := utils.GetPrefixAndMaskFromIPAddress(nbIPAddress.Address)
			if err != nil {
				o.Logger.Warningf(o.Ctx, "error extracting prefix from IP address: %s", err)
			} else if mask != constants.MaxIPv4MaskBits {
				_, err = nbi.AddPrefix(o.Ctx, &objects.Prefix{
					Prefix: prefix,
				})
				if err != nil {
					o.Logger.Warningf(o.Ctx, "adding prefix: %s", err)
				}
			}
		}
	}
	return nil
}

func (o *OVirtSource) collectHostNicsData(nbHost *objects.Device, nbi *inventory.NetboxInventory, nics *ovirtsdk4.HostNicSlice, parent2child map[string][]string, master2slave map[string][]string, nicID2nic map[string]*objects.Interface, processedNicsIDs map[string]bool, nicID2IPv4 map[string]string, nicID2IPv6 map[string]string) error {
	for _, nic := range nics.Slice() {
		nicID, exists := nic.Id()
		if !exists {
			o.Logger.Warning(o.Ctx, "id for oVirt nic with id ", nicID, " is empty. This should not happen! Skipping...")
			continue
		}
		nicName, exists := nic.Name()
		if !exists {
			o.Logger.Warning(o.Ctx, "name for oVirt nic with id ", nicID, " is empty.")
			continue
		}

		// Filter out interfaces with user provided filter
		if utils.FilterInterfaceName(nicName, o.SourceConfig.InterfaceFilter) {
			o.Logger.Debugf(o.Ctx, "interface %s is filtered out with interfaceFilter %s", nicName, o.SourceConfig.InterfaceFilter)
			continue
		}

		// var nicType *objects.InterfaceType
		nicSpeedBips, exists := nic.Speed()
		if !exists {
			o.Logger.Debugf(o.Ctx, "speed for oVirt nic with id %s is empty", nicID)
		}
		nicSpeedKbps := nicSpeedBips / constants.KB

		nicMtu, exists := nic.Mtu()
		if !exists {
			o.Logger.Debugf(o.Ctx, "mtu for oVirt nic with id %s is empty", nicID)
		}

		nicComment, _ := nic.Comment()

		var nicEnabled bool
		ovirtNicStatus, exists := nic.Status()
		if exists {
			switch ovirtNicStatus {
			case ovirtsdk4.NICSTATUS_UP:
				nicEnabled = true
			default:
				nicEnabled = false
			}
		}

		// bridged, exists := nic.Bridged() // TODO: bridged interface
		// if exists {
		// 	if bridged {
		// 		// This interface is bridged
		// 		fmt.Printf("nic[%s] is bridged\n", nicName)
		// 	}
		// }

		// Determine nic type (virtual, physical, bond...)
		var nicType *objects.InterfaceType
		nicBaseInterface, exists := nic.BaseInterface()
		if exists {
			// This interface is a vlan bond. We treat is as a virtual interface
			nicType = &objects.VirtualInterfaceType
			parent2child[nicBaseInterface] = append(parent2child[nicBaseInterface], nicID)
		}

		nicBonding, exists := nic.Bonding()
		if exists {
			// Bond interface, we give it a type of LAG
			nicType = &objects.LAGInterfaceType
			slaves, exists := nicBonding.Slaves()
			if exists {
				for _, slave := range slaves.Slice() {
					master2slave[nicID] = append(master2slave[nicID], slave.MustId())
				}
			}
		}

		if nicType == nil {
			// This is a physical interface.
			nicType = objects.IfaceSpeed2IfaceType[objects.InterfaceSpeed(nicSpeedKbps)]
			if nicType == nil {
				nicType = &objects.OtherInterfaceType
			}
		}

		var nicVlan *objects.Vlan
		vlan, exists := nic.Vlan()
		if exists {
			vlanID, exists := vlan.Id()
			if exists {
				vlanName := o.Networks.Vid2Name[int(vlanID)]
				// Get vlanGroup from relation
				vlanGroup, err := common.MatchVlanToGroup(o.Ctx, nbi, vlanName, o.VlanGroupRelations)
				if err != nil {
					return err
				}
				// Get vlan from inventory
				nicVlan = nbi.VlansIndexByVlanGroupIDAndVID[vlanGroup.ID][int(vlanID)]
			}
		}

		var nicTaggedVlans []*objects.Vlan
		if nicVlan != nil {
			nicTaggedVlans = []*objects.Vlan{nicVlan}
		}

		newInterface := &objects.Interface{
			NetboxObject: objects.NetboxObject{
				Tags:        o.Config.SourceTags,
				Description: nicComment,
				CustomFields: map[string]interface{}{
					constants.CustomFieldSourceName: o.SourceConfig.Name,
				},
			},
			Device:      nbHost,
			Name:        nicName,
			Speed:       objects.InterfaceSpeed(nicSpeedKbps),
			Status:      nicEnabled,
			MTU:         int(nicMtu),
			Type:        nicType,
			TaggedVlans: nicTaggedVlans,
		}

		var err error
		// Extract ip info
		if nicIPv4, exists := nic.Ip(); exists {
			if nicAddress, exists := nicIPv4.Address(); exists {
				mask := 32
				if nicMask, exists := nicIPv4.Netmask(); exists {
					mask, err = utils.MaskToBits(nicMask)
					if err != nil {
						return fmt.Errorf("mask to bits: %s", err)
					}
				}
				ipv4Address := fmt.Sprintf("%s/%d", nicAddress, mask)
				nicID2IPv4[nicID] = ipv4Address
			}
		}
		if nicIPv6, exists := nic.Ipv6(); exists {
			if nicAddress, exists := nicIPv6.Address(); exists {
				mask := 128
				if nicMask, exists := nicIPv6.Netmask(); exists {
					mask, err = utils.MaskToBits(nicMask)
					if err != nil {
						return fmt.Errorf("mask to bits: %s", err)
					}
				}
				ipv6Address := fmt.Sprintf("%s/%d", nicAddress, mask)
				nicID2IPv6[nicID] = ipv6Address
			}
		}

		processedNicsIDs[nicID] = true
		nicID2nic[nicID] = newInterface
	}
	return nil
}

// syncVms synces ovirt vms into netbox inventory.
func (o *OVirtSource) syncVms(nbi *inventory.NetboxInventory) error {
	for vmID, ovirtVM := range o.Vms {
		collectedVM, err := o.extractVMData(nbi, vmID, ovirtVM)
		if err != nil {
			return err
		}

		nbVM, err := nbi.AddVM(o.Ctx, collectedVM)
		if err != nil {
			return fmt.Errorf("failed to sync oVirt vm %s: %v", collectedVM.Name, err)
		}

		err = o.syncVMInterfaces(nbi, ovirtVM, nbVM)
		if err != nil {
			return fmt.Errorf("failed to sync oVirt vm %s's interfaces: %v", collectedVM.Name, err)
		}
	}
	return nil
}

//nolint:gocyclo
func (o *OVirtSource) extractVMData(nbi *inventory.NetboxInventory, vmID string, vm *ovirtsdk4.Vm) (*objects.VM, error) {
	// VM name, which is used as unique identifier for VMs in Netbox
	vmName, exists := vm.Name()
	if !exists {
		o.Logger.Warning(o.Ctx, "name for oVirt vm with id ", vmID, " is empty. VM has to have unique name to be synced to netbox. Skipping...")
	}

	// VM's Cluster
	var vmCluster *objects.Cluster
	cluster, exists := vm.Cluster()
	if exists {
		if _, ok := o.Clusters[cluster.MustId()]; ok {
			vmCluster = nbi.ClustersIndexByName[o.Clusters[cluster.MustId()].MustName()]
		}
	}

	// Get VM's site,tenant and platform from cluster
	var vmTenantGroup *objects.TenantGroup
	var vmTenant *objects.Tenant
	var vmSite *objects.Site
	if vmCluster != nil {
		vmTenantGroup = vmCluster.TenantGroup
		vmTenant = vmCluster.Tenant
		vmSite = vmCluster.Site
	}

	// VM's Status
	var vmStatus *objects.VMStatus
	status, exists := vm.Status()
	if exists {
		switch status {
		case ovirtsdk4.VMSTATUS_UP:
			vmStatus = &objects.VMStatusActive
		default:
			vmStatus = &objects.VMStatusOffline
		}
	}

	// VM's Host Device (server)
	var vmHostDevice *objects.Device
	if host, exists := vm.Host(); exists {
		if oHost, ok := o.Hosts[host.MustId()]; ok {
			if oHostName, ok := oHost.Name(); ok {
				vmHostDevice = nbi.DevicesIndexByNameAndSiteID[oHostName][vmSite.ID]
			}
		}
	}

	// vmVCPUs
	var vmVCPUs float32
	if cpuData, exists := vm.Cpu(); exists {
		if cpuTopology, exists := cpuData.Topology(); exists {
			if cores, exists := cpuTopology.Cores(); exists {
				vmVCPUs = float32(cores)
			}
			if sockets, exists := cpuTopology.Sockets(); exists {
				vmVCPUs *= float32(sockets)
			}
		}
	}

	// Memory
	var vmMemorySizeBytes int64
	if memory, exists := vm.Memory(); exists {
		vmMemorySizeBytes = memory
	}

	// Disks
	var vmDiskSizeBytes int64
	if diskAttachment, exists := vm.DiskAttachments(); exists {
		for _, diskAttachment := range diskAttachment.Slice() {
			if ovirtDisk, exists := diskAttachment.Disk(); exists {
				disk := o.Disks[ovirtDisk.MustId()]
				if provisionedDiskSize, exists := disk.ProvisionedSize(); exists {
					vmDiskSizeBytes += provisionedDiskSize
				}
			}
		}
	}

	// VM's comments
	var vmComments string
	if comments, exists := vm.Comment(); exists {
		vmComments = comments
	}

	// VM's Platform
	var vmPlatform *objects.Platform
	var vmOsType, vmOsVersion, vmCPUArch string
	if guestOs, exists := vm.GuestOperatingSystem(); exists {
		if guestOsType, exists := guestOs.Distribution(); exists {
			vmOsType = guestOsType
		}
		if guestOsVersion, exists := guestOs.Version(); exists {
			if osMajorVersion, exists := guestOsVersion.Major(); exists {
				vmOsVersion = fmt.Sprintf("%d", osMajorVersion)
			}
		}
		if guestArchitecture, exists := guestOs.Architecture(); exists {
			vmCPUArch = guestArchitecture
			if guestArchBits, ok := constants.Arch2Bit[guestArchitecture]; ok {
				vmCPUArch = guestArchBits
			}
		}
	} else {
		if os, exists := vm.Os(); exists {
			if ovirtOsType, exists := os.Type(); exists {
				vmOsType = ovirtOsType
			}
			if ovirtOsVersion, exists := os.Version(); exists {
				if osFullVersion, exists := ovirtOsVersion.FullVersion(); exists {
					vmOsVersion = osFullVersion
				}
			}
			if cpuData, exists := vm.Cpu(); exists {
				if cpuArch, exists := cpuData.Architecture(); exists {
					vmCPUArch = fmt.Sprintf("%s", cpuArch) //nolint:gosimple
				}
			}
		}
	}
	platformName := utils.GeneratePlatformName(vmOsType, vmOsVersion, vmCPUArch)
	vmPlatform, err := nbi.AddPlatform(o.Ctx, &objects.Platform{
		Name: platformName,
		Slug: utils.Slugify(platformName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed adding oVirt vm's Platform %v with error: %s", vmPlatform, err)
	}

	return &objects.VM{
		NetboxObject: objects.NetboxObject{
			Tags: o.Config.SourceTags,
			CustomFields: map[string]interface{}{
				constants.CustomFieldSourceName: o.SourceConfig.Name,
			},
		},
		Name:        vmName,
		Cluster:     vmCluster,
		Site:        vmSite,
		Tenant:      vmTenant,
		TenantGroup: vmTenantGroup,
		Status:      vmStatus,
		Host:        vmHostDevice,
		Platform:    vmPlatform,
		Comments:    vmComments,
		VCPUs:       vmVCPUs,
		Memory:      int(vmMemorySizeBytes / constants.KiB / constants.KiB),               // MBs
		Disk:        int(vmDiskSizeBytes / constants.KiB / constants.KiB / constants.KiB), // GBs
	}, nil
}

// syncVMInterfaces is a helper function for syncVMS. It syncs all interfaces from a VM to netbox.
func (o *OVirtSource) syncVMInterfaces(nbi *inventory.NetboxInventory, ovirtVM *ovirtsdk4.Vm, netboxVM *objects.VM) error {
	err := o.syncVMNics(nbi, ovirtVM, netboxVM)
	if err != nil {
		return fmt.Errorf("sync VMNics %s", err)
	}
	if reportedDevices, exist := ovirtVM.ReportedDevices(); exist {
		for _, reportedDevice := range reportedDevices.Slice() {
			if reportedDeviceType, exist := reportedDevice.Type(); exist {
				if reportedDeviceType == "network" {
					// We add interface to the list
					var vmInterface *objects.VMInterface
					var err error
					vmInterfaceMac := ""
					if macAddressObj, exists := reportedDevice.Mac(); exists {
						if macAddress, exists := macAddressObj.Address(); exists {
							vmInterfaceMac = macAddress
						}
					}
					if reportedDeviceName, exists := reportedDevice.Name(); exists {
						if utils.FilterInterfaceName(reportedDeviceName, o.SourceConfig.InterfaceFilter) {
							o.Logger.Debugf(o.Ctx, "interface %s is filtered out with interfaceFilter %s", reportedDeviceName, o.SourceConfig.InterfaceFilter)
							continue
						}
						vmInterface, err = nbi.AddVMInterface(o.Ctx, &objects.VMInterface{
							NetboxObject: objects.NetboxObject{
								Tags:        o.Config.SourceTags,
								Description: reportedDevice.MustDescription(),
								CustomFields: map[string]interface{}{
									constants.CustomFieldSourceName: o.SourceConfig.Name,
								},
							},
							VM:         netboxVM,
							Name:       reportedDeviceName,
							MACAddress: strings.ToUpper(vmInterfaceMac),
							Enabled:    true, // TODO
						})
						if err != nil {
							return fmt.Errorf("failed to sync oVirt vm %s's interface %s: %v", netboxVM.Name, reportedDeviceName, err)
						}
					} else {
						o.Logger.Warning(o.Ctx, "name for oVirt vm's reported device is empty. Skipping...")
						continue
					}

					if reportedDeviceIps, exist := reportedDevice.Ips(); exist {
						for _, ip := range reportedDeviceIps.Slice() {
							if ipAddress, exists := ip.Address(); exists {
								if ipVersion, exists := ip.Version(); exists {
									// Try to do reverse lookup of IP to get DNS name
									hostname := utils.ReverseLookup(ipAddress)

									// Set default mask
									var ipMask string
									if netMask, exists := ip.Netmask(); exists {
										ipMask = fmt.Sprintf("/%s", netMask)
									} else {
										switch ipVersion {
										case "v4":
											ipMask = "/32"
										case "v6":
											ipMask = "/128"
										}
									}

									if !utils.SubnetsContainIPAddress(ipAddress, o.SourceConfig.IgnoredSubnets) {
										newIPAddress, err := nbi.AddIPAddress(o.Ctx, &objects.IPAddress{
											NetboxObject: objects.NetboxObject{
												Tags: o.Config.SourceTags,
												CustomFields: map[string]interface{}{
													constants.CustomFieldSourceName:   o.SourceConfig.Name,
													constants.CustomFieldArpEntryName: false,
												},
											},
											Address:            ipAddress + ipMask,
											Tenant:             netboxVM.Tenant,
											Status:             &objects.IPAddressStatusActive,
											DNSName:            hostname,
											AssignedObjectType: objects.AssignedObjectTypeVMInterface,
											AssignedObjectID:   vmInterface.ID,
										})

										if err != nil {
											o.Logger.Errorf(o.Ctx, "add ip address: %s", err)
											continue
										}

										// Check if ip is primary
										if ipVersion == "v4" {
											vmIP := utils.Lookup(netboxVM.Name)
											if vmIP != "" && vmIP == ipAddress || netboxVM.PrimaryIPv4 == nil {
												vmCopy := *netboxVM
												vmCopy.PrimaryIPv4 = newIPAddress
												_, err := nbi.AddVM(o.Ctx, &vmCopy)
												if err != nil {
													o.Logger.Errorf(o.Ctx, "adding vm's primary ipv4 address: %s", err)
													continue
												}
											}
										}
										prefix, mask, err := utils.GetPrefixAndMaskFromIPAddress(newIPAddress.Address)
										if err != nil {
											o.Logger.Debugf(o.Ctx, "extract prefix: %s", err)
										} else if mask != constants.MaxIPv4MaskBits {
											_, err = nbi.AddPrefix(o.Ctx, &objects.Prefix{
												Prefix: prefix,
											})
											if err != nil {
												o.Logger.Errorf(o.Ctx, "add prefix: %s", err)
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return nil
}

func (o *OVirtSource) syncVMNics(nbi *inventory.NetboxInventory, ovirtVM *ovirtsdk4.Vm, netboxVM *objects.VM) error {
	if nics, ok := ovirtVM.Nics(); ok {
		for _, nic := range nics.Slice() {
			nicName, ok := nic.Name()
			if !ok {
				o.Logger.Debugf(o.Ctx, "skipping nic because it doesn't have name")
				continue
			}
			if utils.FilterInterfaceName(nicName, o.SourceConfig.InterfaceFilter) {
				o.Logger.Debugf(o.Ctx, "filtering interface %s with interface filter %s", nicName, o.SourceConfig.InterfaceFilter)
				continue
			}
			var nicID string
			if id, ok := nic.Id(); ok {
				nicID = id
			}
			var nicDescription string
			if description, ok := nic.Description(); ok {
				nicDescription = description
			}

			if len(nicDescription) == 0 {
				if comment, ok := nic.Comment(); ok {
					nicDescription = comment
				}
			}

			var nicMAC string
			if mac, ok := nic.Mac(); ok {
				if macAddress, ok := mac.Address(); ok {
					nicMAC = strings.ToUpper(macAddress)
				}
			}

			var nicMode *objects.VMInterfaceMode
			var nicVlans []*objects.Vlan
			if vnicProfile, ok := nic.VnicProfile(); ok {
				if vnicProfileID, ok := vnicProfile.Id(); ok {
					// Get network for profile
					vnicNetwork := o.Networks.OVirtNetworks[o.Networks.VnicProfile2Network[vnicProfileID]]
					if vnicNetworkVlan, ok := vnicNetwork.Vlan(); ok {
						if vlanID, ok := vnicNetworkVlan.Id(); ok {
							vlanName := o.Networks.Vid2Name[int(vlanID)]
							vlanGroup, err := common.MatchVlanToGroup(o.Ctx, nbi, vlanName, o.VlanGroupRelations)
							if err != nil {
								o.Logger.Warningf(o.Ctx, "match vlan to group: %s", err)
								continue
							}
							nicVlans = []*objects.Vlan{nbi.VlansIndexByVlanGroupIDAndVID[vlanGroup.ID][int(vlanID)]}
							nicMode = &objects.VMInterfaceModeTagged
						}
					}
				}
			}

			_, err := nbi.AddVMInterface(o.Ctx, &objects.VMInterface{
				NetboxObject: objects.NetboxObject{
					Tags:        o.SourceTags,
					Description: nicDescription,
					CustomFields: map[string]interface{}{
						constants.CustomFieldSourceIDName: nicID,
					},
				},
				VM:          netboxVM,
				Name:        nicName,
				MACAddress:  nicMAC,
				Mode:        nicMode,
				Enabled:     true,
				TaggedVlans: nicVlans,
			})
			if err != nil {
				return fmt.Errorf("add vm interface: %s", err)
			}
		}
	}
	return nil
}
