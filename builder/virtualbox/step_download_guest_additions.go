package virtualbox

import (
	"bytes"
	"fmt"
	"github.com/mitchellh/multistep"
	"github.com/mitchellh/packer/common"
	"github.com/mitchellh/packer/packer"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"
)

var additionsVersionMap = map[string]string{
	"4.2.1":  "4.2.0",
	"4.1.23": "4.1.22",
}

type guestAdditionsUrlTemplate struct {
	Version string
}

// This step uploads a file containing the VirtualBox version, which
// can be useful for various provisioning reasons.
//
// Produces:
//   guest_additions_path string - Path to the guest additions.
type stepDownloadGuestAdditions struct{}

func (s *stepDownloadGuestAdditions) Run(state map[string]interface{}) multistep.StepAction {
	var action multistep.StepAction
	driver := state["driver"].(Driver)
	ui := state["ui"].(packer.Ui)
	config := state["config"].(*config)

	// Get VBox version
	version, err := driver.Version()
	if err != nil {
		state["error"] = fmt.Errorf("Error reading version for guest additions download: %s", err)
		return multistep.ActionHalt
	}

	if newVersion, ok := additionsVersionMap[version]; ok {
		log.Printf("Rewriting guest additions version: %s to %s", version, newVersion)
		version = newVersion
	}

	additionsName := fmt.Sprintf("VBoxGuestAdditions_%s.iso", version)

	// Use provided version or get it from virtualbox.org
	var checksum string

	if config.GuestAdditionsSHA256 != "" {
		checksum = config.GuestAdditionsSHA256
	} else {
		checksum, action = s.downloadAdditionsSHA256(state, version, additionsName)
		if action != multistep.ActionContinue {
			return action
		}
	}

	// Use the provided source (URL or file path) or generate it
	url := config.GuestAdditionsURL
	if url != "" {
		tplData := &guestAdditionsUrlTemplate{
			Version: version,
		}

		url, err = config.tpl.Process(url, tplData)
		if err != nil {
			err := fmt.Errorf("Error preparing guest additions url: %s", err)
			state["error"] = err
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
	} else {
		url = fmt.Sprintf(
			"http://download.virtualbox.org/virtualbox/%s/%s",
			version,
			additionsName)
	}

	url, err = common.DownloadableURL(url)
	if err != nil {
		err := fmt.Errorf("Error preparing guest additions url: %s", err)
		state["error"] = err
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	log.Printf("Guest additions URL: %s", url)

	downStep := &common.StepDownload{
		Checksum:     checksum,
		ChecksumType: "sha256",
		Description:  "Guest additions",
		ResultKey:    "guest_additions_path",
		Url:          []string{url},
	}

	return downStep.Run(state)
}

func (s *stepDownloadGuestAdditions) Cleanup(state map[string]interface{}) {}

func (s *stepDownloadGuestAdditions) downloadAdditionsSHA256(state map[string]interface{}, additionsVersion string, additionsName string) (string, multistep.StepAction) {
	// First things first, we get the list of checksums for the files available
	// for this version.
	checksumsUrl := fmt.Sprintf(
		"http://download.virtualbox.org/virtualbox/%s/SHA256SUMS",
		additionsVersion)

	checksumsFile, err := ioutil.TempFile("", "packer")
	if err != nil {
		state["error"] = fmt.Errorf(
			"Failed creating temporary file to store guest addition checksums: %s",
			err)
		return "", multistep.ActionHalt
	}
	defer os.Remove(checksumsFile.Name())
	checksumsFile.Close()

	downStep := &common.StepDownload{
		Description: "Guest additions checksums",
		ResultKey:   "guest_additions_checksums_path",
		TargetPath:  checksumsFile.Name(),
		Url:         []string{checksumsUrl},
	}

	action := downStep.Run(state)
	if action == multistep.ActionHalt {
		return "", action
	}

	// Next, we find the checksum for the file we're looking to download.
	// It is an error if the checksum cannot be found.
	checksumsF, err := os.Open(state["guest_additions_checksums_path"].(string))
	if err != nil {
		state["error"] = fmt.Errorf("Error opening guest addition checksums: %s", err)
		return "", multistep.ActionHalt
	}
	defer checksumsF.Close()

	// We copy the contents of the file into memory. In general this file
	// is quite small so that is okay. In the future, we probably want to
	// use bufio and iterate line by line.
	var contents bytes.Buffer
	io.Copy(&contents, checksumsF)

	checksum := ""
	for _, line := range strings.Split(contents.String(), "\n") {
		parts := strings.Fields(line)
		log.Printf("Checksum file parts: %#v", parts)
		if len(parts) != 2 {
			// Bogus line
			continue
		}

		if strings.HasSuffix(parts[1], additionsName) {
			checksum = parts[0]
			log.Printf("Guest additions checksum: %s", checksum)
			break
		}
	}

	if checksum == "" {
		state["error"] = fmt.Errorf(
			"The checksum for the file '%s' could not be found.", additionsName)
		return "", multistep.ActionHalt
	}

	return checksum, multistep.ActionContinue

}
