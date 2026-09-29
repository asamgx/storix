package catalog

import "github.com/asamgx/storix/internal/classify"

// containerRules are the static fallbacks for container runtimes and virtual
// machines. Their disk images are sparse, so the bytes here are the allocated
// ones: what the host has actually given the runtime, not what the guest
// believes it has. The detectors of M9 add the guest's own reading beside it.
var containerRules = []classify.Rule{
	{
		ID: "ctr.orbstack.group", Match: "~/Library/Group Containers/HUAQ24HBR6.dev.orbstack", Bucket: containers,
		Category: "OrbStack", Owner: "OrbStack",
		OwnerKeys: []string{"app:dev.orbstack.OrbStack", "app:dev.kdrag0n.MacVirt", "team:HUAQ24HBR6"},
		Reclaim:   tool,
		Explain:   "the OrbStack disk image and swap; OrbStack shrinks it automatically",
	},
	{
		ID: "ctr.orbstack.home", Match: "~/.orbstack", Bucket: containers,
		Category: "OrbStack", Owner: "OrbStack",
		OwnerKeys: []string{"app:dev.orbstack.OrbStack"}, Reclaim: regen,
		Explain: "OrbStack's configuration, sockets and logs",
	},
	{
		ID: "ctr.orbstack.support", Match: "~/Library/Application Support/OrbStack", Bucket: containers,
		Category: "OrbStack", Owner: "OrbStack",
		OwnerKeys: []string{"app:dev.orbstack.OrbStack"}, Reclaim: unsure,
		Explain: "OrbStack's application data",
	},
	{
		ID: "ctr.orbstack.cache", Match: "~/Library/Caches/dev.orbstack.OrbStack", Bucket: containers,
		Category: "OrbStack", Owner: "OrbStack",
		OwnerKeys: []string{"app:dev.orbstack.OrbStack"}, Reclaim: regen,
		Explain: "OrbStack's cache",
	},
	{
		ID: "ctr.orbstack.macvirt-cache", Match: "~/Library/Caches/dev.kdrag0n.MacVirt", Bucket: containers,
		Category: "OrbStack", Owner: "OrbStack",
		OwnerKeys: []string{"app:dev.kdrag0n.MacVirt"}, Reclaim: regen,
		Explain: "the OrbStack helper's cache",
	},
	{
		ID: "ctr.orbstack.mount", Match: "~/OrbStack", Bucket: containers,
		Category: "OrbStack", Owner: "OrbStack",
		OwnerKeys: []string{"app:dev.orbstack.OrbStack"}, Reclaim: tool,
		Explain: "the NFS view of the OrbStack machines; its bytes live in the disk image, so the walk does not enter it",
	},

	{
		ID: "ctr.docker.container", Match: "~/Library/Containers/com.docker.docker", Bucket: containers,
		Category: "Docker Desktop", Owner: "Docker Desktop",
		OwnerKeys: []string{"app:com.docker.docker"}, Reclaim: tool,
		Explain: "Docker Desktop's virtual machine disk (Docker.raw); `docker system prune` frees space inside it",
	},
	{
		ID: "ctr.docker.group", Match: "~/Library/Group Containers/group.com.docker", Bucket: containers,
		Category: "Docker Desktop", Owner: "Docker Desktop",
		OwnerKeys: []string{"app:com.docker.docker"}, Reclaim: tool,
		Explain: "Docker Desktop's shared settings and caches",
	},
	{
		ID: "ctr.docker.home", Match: "~/.docker", Bucket: containers,
		Category: "Docker", Owner: "Docker", OwnerKeys: []string{"cli:docker"}, Reclaim: regen,
		Explain: "the Docker CLI's configuration, contexts, buildx state and Scout cache",
	},

	{
		ID: "ctr.colima.home", Match: "~/.colima", Bucket: containers,
		Category: "Colima", Owner: "Colima", OwnerKeys: []string{"cli:colima"}, Reclaim: tool,
		Explain: "Colima's virtual machine disks",
	},
	{
		ID: "ctr.lima.home", Match: "~/.lima", Bucket: containers,
		Category: "Lima", Owner: "Lima", OwnerKeys: []string{"cli:limactl"}, Reclaim: tool,
		Explain: "Lima's virtual machine disks",
	},
	{
		ID: "ctr.podman.machine", Match: "~/.local/share/containers", Bucket: containers,
		Category: "Podman", Owner: "Podman", OwnerKeys: []string{"cli:podman"}, Reclaim: tool,
		Explain: "Podman's machine images and storage",
	},
	{
		ID: "ctr.podman.config", Match: "~/.config/containers", Bucket: containers,
		Category: "Podman", Owner: "Podman", OwnerKeys: []string{"cli:podman"}, Reclaim: user,
		Explain: "Podman's configuration",
	},

	{
		ID: "ctr.kube.home", Match: "~/.kube", Bucket: containers,
		Category: "Kubernetes", Owner: "kubectl", OwnerKeys: []string{"cli:kubectl"}, Reclaim: user,
		Explain: "kubeconfig and the discovery cache",
	},
	{
		ID: "ctr.kube.cache", Match: "~/.kube/cache", Bucket: containers,
		Category: "Kubernetes", Owner: "kubectl", OwnerKeys: []string{"cli:kubectl"}, Reclaim: regen,
		Explain: "kubectl's API discovery cache",
	},
	{
		ID: "ctr.kube.minikube", Match: "~/.minikube", Bucket: containers,
		Category: "Kubernetes", Owner: "minikube", OwnerKeys: []string{"cli:minikube"}, Reclaim: tool,
		Explain: "minikube's cluster disks and cached images",
	},
	{
		ID: "ctr.kube.kind", Match: "~/.kind", Bucket: containers,
		Category: "Kubernetes", Owner: "kind", OwnerKeys: []string{"cli:kind"}, Reclaim: tool,
		Explain: "kind's cluster state",
	},
	{
		ID: "ctr.kube.rancher", Match: "~/.rd", Bucket: containers,
		Category: "Kubernetes", Owner: "Rancher Desktop",
		OwnerKeys: []string{"app:io.rancherdesktop.app"}, Reclaim: tool,
		Explain: "Rancher Desktop's virtual machine and images",
	},

	{
		ID: "ctr.vm.utm", Match: "~/Library/Containers/com.utmapp.UTM", Bucket: containers,
		Category: "Virtual machines", Owner: "UTM", OwnerKeys: []string{"app:com.utmapp.UTM"}, Reclaim: user,
		Explain: "UTM virtual machines; their disks are your data",
	},
	{
		ID: "ctr.vm.parallels", Match: "~/Parallels", Bucket: containers,
		Category: "Virtual machines", Owner: "Parallels",
		OwnerKeys: []string{"app:com.parallels.desktop.console"}, Reclaim: user,
		Explain: "Parallels virtual machines",
	},
	{
		ID: "ctr.vm.parallels-library", Match: "~/Library/Parallels", Bucket: containers,
		Category: "Virtual machines", Owner: "Parallels",
		OwnerKeys: []string{"app:com.parallels.desktop.console"}, Reclaim: regen,
		Explain: "Parallels' caches and installer images",
	},
	{
		ID: "ctr.vm.vmware", Match: "~/Virtual Machines.localized", Bucket: containers,
		Category: "Virtual machines", Owner: "VMware Fusion",
		OwnerKeys: []string{"app:com.vmware.fusion"}, Reclaim: user,
		Explain: "VMware Fusion virtual machines",
	},
	{
		ID: "ctr.vm.virtualbox", Match: "~/VirtualBox VMs", Bucket: containers,
		Category: "Virtual machines", Owner: "VirtualBox",
		OwnerKeys: []string{"app:org.virtualbox.app.VirtualBox"}, Reclaim: user,
		Explain: "VirtualBox virtual machines",
	},
	{
		ID: "ctr.vm.vagrant", Match: "~/.vagrant.d", Bucket: containers,
		Category: "Virtual machines", Owner: "Vagrant", OwnerKeys: []string{"cli:vagrant"}, Reclaim: tool,
		Explain: "Vagrant boxes; `vagrant box prune` removes old ones",
	},
}
