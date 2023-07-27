package main

import (
	"context"
	"errors"
	"time"
	"bufio"
	"os"
	"os/user"
	"fmt"
	"strings"
	"strconv"

	"github.com/docker/docker/client"
	"github.com/docker/docker/api/types/container"
	mountType "github.com/docker/docker/api/types/mount"
	network "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types"
	"github.com/docker/go-connections/nat"
	"log"
)

var DockerNetworkLock = make(chan bool, 1)

var Reset  = "\033[0m"
var Red    = "\033[31m"
var Green  = "\033[32m"
var Yellow = "\033[33m"
var Blue   = "\033[34m"
var Purple = "\033[35m"
var Cyan   = "\033[36m"
var Gray   = "\033[37m"
var White  = "\033[97m"

const (
	DEBUG = 0
	INFO = 1
	WARNING = 2
	ERROR = 3
)

func Debug(message string) {
	ll := 0
	if ll <= DEBUG {
		log.Println(Purple + "[DEBUG] " + message + Reset)
	}
}

func Log(message string) {
	ll := 0
	if ll <= INFO {
		log.Println(Blue + "[INFO] " + message + Reset)
	}
}

func Warn(message string) {
	ll := 0
	if ll <= WARNING {
		log.Println(Yellow + "[WARN] " + message + Reset)
	}
}

func Error(message string, err error) {
	ll := 0
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	if ll <= ERROR {
		log.Println(Red + "[ERROR] " + message + " : " + errStr + Reset)
	}
}

func Fatal(message string, err error) {
	ll := 0
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	if ll <= ERROR {
		log.Fatal(Red + "[FATAL] " + message + " : " + errStr + Reset)
	}
}

var DockerClient *client.Client
var DockerContext context.Context
var DockerNetworkName = "cosmos-network"

func getIdFromName(name string) (string, error) {
	containers, err := DockerClient.ContainerList(DockerContext, types.ContainerListOptions{})
	if err != nil {
		Error("Docker Container List", err)
		return "", err
	}

	for _, container := range containers {
		if container.Names[0] == name {
			Warn(container.Names[0] + " == " + name + " == " + container.ID)
			return container.ID, nil
		}
	}

	return "", errors.New("Container not found")
}

func IsConnectedToNetwork(containerConfig types.ContainerJSON, networkName string) bool {
	if(containerConfig.NetworkSettings == nil) {
		return false
	}

	for name, _ := range containerConfig.NetworkSettings.Networks {
		if name == networkName {
			return true
		}
	}

	return false
}

func ConnectToNetworkSync(networkName string, containerID string) error {
	err := DockerClient.NetworkConnect(DockerContext, networkName, containerID, &network.EndpointSettings{})
	if err != nil {
		Error("ConnectToNetworkSync", err)
		return err
	}

	// wait for connection to be established
	retries := 10
	for {
		newContainer, err := DockerClient.ContainerInspect(DockerContext, containerID)
		if err != nil {
			Error("ConnectToNetworkSync", err)
			return err
		}
		
		if(IsConnectedToNetwork(newContainer, networkName)) {
			break
		}

		retries--
		if retries == 0 {
			return errors.New("ConnectToNetworkSync: Timeout")
		}
		time.Sleep(500 * time.Millisecond)
	}
	

	Log("Connected "+containerID+" to network: " + networkName)

	return nil
}

var DockerIsConnected = false

func Connect() error {
	if DockerClient != nil {
		// check if connection is still alive
		ping, err := DockerClient.Ping(DockerContext)
		if ping.APIVersion != "" && err == nil {
			DockerIsConnected = true
			return nil
		} else {
			DockerIsConnected = false
			DockerClient = nil
			Error("Docker Connection died, will try to connect again", err)
		}
	}
	if DockerClient == nil {
		ctx := context.Background()
		client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			DockerIsConnected = false
			return err
		}
		defer client.Close()

		DockerClient = client
		DockerContext = ctx

		ping, err := DockerClient.Ping(DockerContext)
		if ping.APIVersion != "" && err == nil {
			DockerIsConnected = true
			Log("Docker Connected")
		} else {
			DockerIsConnected = false
			Error("Docker Connection - Cannot ping Daemon. Is it running?", nil)
			return errors.New("Docker Connection - Cannot ping Daemon. Is it running?")
		}
		
		// if running in Docker, connect to main network
		// if os.Getenv("HOSTNAME") != "" {
		// 	ConnectToNetwork(os.Getenv("HOSTNAME"))
		// }
	}

	return nil
}

func EditContainer(oldContainerID string, newConfig types.ContainerJSON, noLock bool) (string, error) {
	if(oldContainerID != "" && !noLock) {
		// no need to re-lock if we are reverting
		DockerNetworkLock <- true
		defer func() { 
			<-DockerNetworkLock 
			Debug("Unlocking EDIT Container")
		}()

		errD := Connect()
		if errD != nil {
			return "", errD
		}
	}

	if(newConfig.HostConfig.NetworkMode != "bridge" &&
		 newConfig.HostConfig.NetworkMode != "default" &&
		 newConfig.HostConfig.NetworkMode != "host" &&
		 newConfig.HostConfig.NetworkMode != "none") {
			if(!HasLabel(newConfig, "cosmos-force-network-mode")) {
				AddLabels(newConfig, map[string]string{"cosmos-force-network-mode": string(newConfig.HostConfig.NetworkMode)})
			} else {
				newConfig.HostConfig.NetworkMode = container.NetworkMode(GetLabel(newConfig, "cosmos-force-network-mode"))
			}
	}
	
	newName := newConfig.Name
	oldContainer := newConfig

	if(oldContainerID != "") {
		// create missing folders
		
		for _, newmount := range newConfig.HostConfig.Mounts {
			if newmount.Type == mountType.TypeBind {
				newSource := newmount.Source

				if os.Getenv("HOSTNAME") != "" {
					if _, err := os.Stat("/mnt/host"); os.IsNotExist(err) {
						Error("EditContainer: Unable to create directory for bind mount in the host directory. Please mount the host / in Cosmos with  -v /:/mnt/host to enable folder creations, or create the bind folder yourself", err)
						return "", errors.New("Unable to create directory for bind mount in the host directory. Please mount the host / in Cosmos with  -v /:/mnt/host to enable folder creations, or create the bind folder yourself")
					}
					newSource = "/mnt/host" + newSource
				}
						
				Log(fmt.Sprintf("Checking directory %s for bind mount", newSource))

				if _, err := os.Stat(newSource); os.IsNotExist(err) {
					Log(fmt.Sprintf("Not found. Creating directory %s for bind mount", newSource))
	
					err := os.MkdirAll(newSource, 0755)
					if err != nil {
						Error("EditContainer: Unable to create directory for bind mount", err)
						return "", errors.New("Unable to create directory for bind mount. Make sure parent directories exist, and that Cosmos has permissions to create directories in the host directory")
					}
		
					if newConfig.Config.User != "" {
						// Change the ownership of the directory to the container.User
						userInfo, err := user.Lookup(newConfig.Config.User)
						if err != nil {
							Error("EditContainer: Unable to lookup user", err)
						} else {
							uid, _ := strconv.Atoi(userInfo.Uid)
							gid, _ := strconv.Atoi(userInfo.Gid)
							err = os.Chown(newSource, uid, gid)
							if err != nil {
								Error("EditContainer: Unable to change ownership of directory", err)
							}
						}	
					}
				}
			}
		}

		Log("EditContainer - Container updating. Retriveing currently running " + oldContainerID)

		var err error

		// get container informations
		// https://godoc.org/github.com/docker/docker/api/types#ContainerJSON
		oldContainer, err = DockerClient.ContainerInspect(DockerContext, oldContainerID)

		if err != nil {
			return "", err
		}

		// check if new image exists, if not, pull it
		_, _, errImage := DockerClient.ImageInspectWithRaw(DockerContext, newConfig.Config.Image)
		if errImage != nil {
			Log("EditContainer - Image not found, pulling " + newConfig.Config.Image)
			out, errPull := DockerClient.ImagePull(DockerContext, newConfig.Config.Image, types.ImagePullOptions{})
			if errPull != nil {
				Error("EditContainer - Image not found.", errPull)
				return "", errors.New("Image not found. " + errPull.Error())
			}
			defer out.Close()

			// wait for image pull to finish
			scanner := bufio.NewScanner(out)
			for scanner.Scan() {
				Log(scanner.Text())
			}
		}

		// if no name, use the same one, that will force Docker to create a hostname if not set
		newName = oldContainer.Name

		// stop and remove container
		stopError := DockerClient.ContainerStop(DockerContext, oldContainerID, container.StopOptions{})
		if stopError != nil {
			return "", stopError
		}

		removeError := DockerClient.ContainerRemove(DockerContext, oldContainerID, types.ContainerRemoveOptions{})
		if removeError != nil {
			return "", removeError
		}

		// wait for container to be destroyed
		//
		for {
			_, err := DockerClient.ContainerInspect(DockerContext, oldContainerID)
			if err != nil {
				break
			} else {
				Log("EditContainer - Waiting for container to be destroyed")
				time.Sleep(1 * time.Second)
			}
		}

		Log("EditContainer - Container stopped " + oldContainerID)
	} else {
		Log("EditContainer - Revert started")
	}
	
	// only force hostname if network is bridge or default, otherwise it will fail
	if newConfig.HostConfig.NetworkMode == "bridge" || newConfig.HostConfig.NetworkMode == "default" {
		newConfig.Config.Hostname = newName[1:]
	} else {
		// if not, remove hostname because otherwise it will try to keep the old one
		newConfig.Config.Hostname = ""
		// IDK Docker is weird, if you don't erase this it will break
		newConfig.Config.ExposedPorts = nil
	}
	
	// recreate container with new informations
	createResponse, createError := DockerClient.ContainerCreate(
		DockerContext,
		newConfig.Config,
		newConfig.HostConfig,
		nil,
		nil,
		newName,
	)
	if createError != nil {
		Error("EditContainer - Failed to create container", createError)
	}
	
	Log("EditContainer - Container recreated. Re-connecting networks " + createResponse.ID)

	// is force secure
	isForceSecure := newConfig.Config.Labels["cosmos-force-network-secured"] == "true"
	
	// re-connect to networks
	for networkName, _ := range oldContainer.NetworkSettings.Networks {
		if(isForceSecure && networkName == "bridge") {
			Log("EditContainer - Skipping network " + networkName + " (cosmos-force-network-secured is true)")
			continue
		}
		Log("EditContainer - Connecting to network " + networkName)
		errNet := ConnectToNetworkSync(networkName, createResponse.ID)
		if errNet != nil {
			Error("EditContainer - Failed to connect to network " + networkName, errNet)
		} else {
			Debug("EditContainer - New Container connected to network " + networkName)
		}
	}
	
	Log("EditContainer - Networks Connected. Starting new container " + createResponse.ID)

	runError := DockerClient.ContainerStart(DockerContext, createResponse.ID, types.ContainerStartOptions{})

	if runError != nil {
		Error("EditContainer - Failed to run container", runError)
	}

	if createError != nil || runError != nil {
		if(oldContainerID == "") {
			if(createError == nil) {
				Error("EditContainer - Failed to revert. Container is re-created but in broken state.", runError)
				return "", runError
			} else {
				Error("EditContainer - Failed to revert. Giving up.", createError)
				return "", createError
			}
		}

		Log("EditContainer - Failed to edit, attempting to revert changes")

		if(createError == nil) {
			Log("EditContainer - Killing new broken container")
			// attempt kill
			DockerClient.ContainerKill(DockerContext, oldContainerID, "")
			DockerClient.ContainerKill(DockerContext, createResponse.ID, "")
			// attempt remove in case created state
			DockerClient.ContainerRemove(DockerContext, oldContainerID, types.ContainerRemoveOptions{})
			DockerClient.ContainerRemove(DockerContext, createResponse.ID, types.ContainerRemoveOptions{})
		}

		Log("EditContainer - Reverting...")
		// attempt to restore container
		restored, restoreError := EditContainer("", oldContainer, false)

		if restoreError != nil {
			Error("EditContainer - Failed to restore container", restoreError)

			if createError != nil {
				Error("EditContainer - re-create container ", createError)
				return "", createError
			} else {
				Error("EditContainer - re-start container ", runError)
				return "", runError
			}
		} else {
			Log("EditContainer - Container restored " + oldContainerID)
			errorWas := ""
			if createError != nil {
				errorWas = createError.Error()
			} else {
				errorWas = runError.Error()
			}
			return restored, errors.New("Failed to edit container, but restored to previous state. Error was: " + errorWas)
		}
	}
	
	// Recreating dependant containers
	Debug("Unlocking EDIT Container")

	if oldContainerID != "" {
		RecreateDepedencies(oldContainerID)
	}

	Log("EditContainer - Container started. All done! " + createResponse.ID)

	return createResponse.ID, nil
}

func RecreateDepedencies(containerID string) {
	containers, err := ListContainers()
	if err != nil {
		Error("RecreateDepedencies", err)
		return
	}

	for _, container := range containers {
		if container.ID == containerID {
			continue
		}

		fullContainer, err := DockerClient.ContainerInspect(DockerContext, container.ID)
		if err != nil {
			Error("RecreateDepedencies", err)
			continue
		}

		// check if network mode contains containerID
		if strings.Contains(string(fullContainer.HostConfig.NetworkMode), containerID) {
			Log("RecreateDepedencies - Recreating " + container.Names[0])
			_, err := EditContainer(container.ID, fullContainer, true)
			if err != nil {
				Error("RecreateDepedencies - Failed to update - ", err)
			}
		}
	}
}

func ListContainers() ([]types.Container, error) {
	errD := Connect()
	if errD != nil {
		return nil, errD
	}

	containers, err := DockerClient.ContainerList(DockerContext, types.ContainerListOptions{
		All: true,
	})
	if err != nil {
		return nil, err
	}

	return containers, nil
}

func AddLabels(containerConfig types.ContainerJSON, labels map[string]string) error {
	for key, value := range labels {
		containerConfig.Config.Labels[key] = value
	}

	return nil
}

func RemoveLabels(containerConfig types.ContainerJSON, labels []string) error {
	for _, label := range labels {
		delete(containerConfig.Config.Labels, label)
	}

	return nil
}

func IsLabel(containerConfig types.ContainerJSON, label string) bool {
	if containerConfig.Config.Labels[label] == "true" {
		return true
	}
	return false
}
func HasLabel(containerConfig types.ContainerJSON, label string) bool {
	if containerConfig.Config.Labels[label] != "" {
		return true
	}
	return false
}
func GetLabel(containerConfig types.ContainerJSON, label string) string {
	return containerConfig.Config.Labels[label]
}

func Test() error {

	// connect()

	// jellyfin, _ := DockerClient.ContainerInspect(DockerContext, "jellyfin")
	// ports := GetAllPorts(jellyfin)
	// fmt.Println(ports)

	// json jellyfin

	// fmt.Println(jellyfin.NetworkSettings)

	return nil
}


func CheckUpdatesAvailable() map[string]bool {
	result := make(map[string]bool)

	// for each containers
	containers, err := ListContainers()
	if err != nil {
		Error("CheckUpdatesAvailable", err)
		return result
	}

	for _, container := range containers {
		Log("Checking for updates for " + container.Image)
		
		fullContainer, err := DockerClient.ContainerInspect(DockerContext, container.ID)
		if err != nil {
			Error("CheckUpdatesAvailable", err)
			continue
		}

		// check container is running 
		if container.State != "running" {
			Log("Container " + container.Names[0] + " is not running, skipping")
			continue
		}

		rc, err := DockerClient.ImagePull(DockerContext, container.Image, types.ImagePullOptions{})
		if err != nil {
			Error("CheckUpdatesAvailable", err)
			continue
		}

		scanner := bufio.NewScanner(rc)
		defer  rc.Close()

		needsUpdate := false

		for scanner.Scan() {
			newStr := scanner.Text()
			// Check if a download has started
			if strings.Contains(newStr, "\"status\":\"Pulling fs layer\"") {
				Log("Updates available for " + container.Image)

				result[container.Names[0]] = true
				if !IsLabel(fullContainer, "cosmos-auto-update") {
					rc.Close()
					break
				} else {
					needsUpdate = true
				}
			} else if strings.Contains(newStr, "\"status\":\"Status: Image is up to date") {
				Log("No updates available for " + container.Image)
				
				if !IsLabel(fullContainer, "cosmos-auto-update") {
					rc.Close()
					break
				}
			} else {
				Log(newStr)
			}
		}

		// no new image to pull, see if local image is matching
		if !result[container.Names[0]] && !needsUpdate {
			// check sum of local vs container image
			Log("CheckUpdatesAvailable - Checking local image for change for " + container.Image)
			localImage, _, err := DockerClient.ImageInspectWithRaw(DockerContext, container.Image)
			if err != nil {
				Error("CheckUpdatesAvailable - local image - ", err)
				continue
			}

			if localImage.ID != container.ImageID {
				result[container.Names[0]] = true
				needsUpdate = true
				Log("CheckUpdatesAvailable - Local updates available for " + container.Image)
			} else {
				Log("CheckUpdatesAvailable - No local updates available for " + container.Image)
			}
		}

		if needsUpdate && IsLabel(fullContainer, "cosmos-auto-update") {
			Log("Downlaoded new update for " + container.Image + " ready to install")
			_, err := EditContainer(container.ID, fullContainer, false)
			if err != nil {
				Error("CheckUpdatesAvailable - Failed to update - ", err)
			} else {
				result[container.Names[0]] = false
			}
		}
	}

	return result
}

func main() {
	// Read container name from env var
	containerName := os.Getenv("CONTAINER_NAME")
	action := os.Getenv("ACTION")

	if containerName == "" {
		Error("No container name specified", nil)
		time.Sleep(60 * time.Minute)
		return
	}

	if action == "" {
		Error("No action specified", nil)
		time.Sleep(60 * time.Minute)
		return
	}

	// connect to docker
	err := Connect()
	if err != nil {
		Error("Failed to connect to docker - ", err)
		time.Sleep(60 * time.Minute)
		return
	}

	// inspect container if found
	container, err := DockerClient.ContainerInspect(DockerContext, containerName)
	if err != nil {
		Error("Failed to inspect container - ", err)
		time.Sleep(60 * time.Minute)
		return
	}
	
	container.HostConfig.NetworkMode = "bridge"

	if action == "ports" {
		Log("Updating ports for " + containerName)

		portslist := os.Getenv("PORTS")
		ports := strings.Split(portslist, ",")

		Log("Ports to use: " + portslist)
		
		// inspect container
		inspect, err := DockerClient.ContainerInspect(DockerContext, containerName)
		if err != nil {
			Error("Failed to inspect container - ", err)
			time.Sleep(60 * time.Minute)
			return
		}
		
		// Update the container
		inspect.HostConfig.PortBindings = map[nat.Port][]nat.PortBinding{}
		inspect.Config.ExposedPorts = make(map[nat.Port]struct{})

		hostPortsBound := make(map[string]bool)

		for _, port := range ports {
			caca := strings.Split(port, ":")
			hostPort, contPort := caca[0], caca[1]
			
			if hostPortsBound[hostPort] {
				Warn("Port " + hostPort + " already bound, skipping")
				continue
			}

			Log("Adding port " + hostPort + " to " + contPort)

			// Get the existing bindings for this container port, if any
			bindings := inspect.HostConfig.PortBindings[nat.Port(contPort)]

			// Append a new PortBinding to the slice of bindings
			bindings = append(bindings, nat.PortBinding{
				HostPort: hostPort,
			})

			// Update the port bindings for this container port
			inspect.HostConfig.PortBindings[nat.Port(contPort)] = bindings

			// Mark the container port as exposed
			inspect.Config.ExposedPorts[nat.Port(contPort)] = struct{}{}

			hostPortsBound[hostPort] = true
		}
		
		// recreate container by calling edit container
		_, err = EditContainer(container.ID, inspect, false)

		if err != nil {
			Error("Failed to update container - ", err)
			time.Sleep(60 * time.Minute)
			return
		}

		Log("Container updated successfully")
		return
	}


	// if action == "update" then pull image
	if action == "update" {
		Log("Checking for updates for " + container.Config.Image)
		rc, err := DockerClient.ImagePull(DockerContext, container.Config.Image, types.ImagePullOptions{})
		if err != nil {
			Error("Failed to pull image - ", err)
			time.Sleep(60 * time.Minute)
			return
		}

		scanner := bufio.NewScanner(rc)
		defer  rc.Close()

		for scanner.Scan() {
			newStr := scanner.Text()
			// Check if a download has started
			if strings.Contains(newStr, "\"status\":\"Pulling fs layer\"") {
				Log("Updates available for " + container.Config.Image)
			} else if strings.Contains(newStr, "\"status\":\"Status: Image is up to date") {
				Log("No updates available for " + container.Config.Image)
			} else {
				Log(newStr)
			}
		}

		// recreate container by calling edit container
		_, err = EditContainer(container.ID, container, false)

		if err != nil {
			Error("Failed to update container - ", err)
			time.Sleep(60 * time.Minute)
			return
		}

		Log("Container updated successfully")
	}

	if action == "recreate" {
		// recreate container by calling edit container
		_, err = EditContainer(container.ID, container, false)

		if err != nil {
			Error("Failed to update container - ", err)
			time.Sleep(60 * time.Minute)
			return
		}

		Log("Container updated successfully")
	}
}