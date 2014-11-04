package stager_docker

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"time"

	"github.com/pivotal-golang/lager"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/cc_messages"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/router"
	"github.com/cloudfoundry-incubator/stager/stager"
	"github.com/cloudfoundry/gunk/urljoiner"
)

const TaskDomain = "cf-app-docker-staging"
const DockerCircusFilename = "docker-circus.zip"

type DockerStager interface {
	Stage(cc_messages.DockerStagingRequestFromCC) error
}

type stager_docker struct {
	logger         lager.Logger
	config         stager.Config
	diegoAPIClient receptor.Client
}

var TailorExecutablePath = "/tmp/docker-circus/tailor"
var TailorOutputPath = "/tmp/docker-result/result.json"

var ErrNoCompilerDefined = errors.New("no compiler defined for requested stack")

func New(diegoAPIClient receptor.Client, logger lager.Logger, config stager.Config) DockerStager {
	return &stager_docker{
		logger:         logger,
		config:         config,
		diegoAPIClient: diegoAPIClient,
	}
}

func (stager *stager_docker) Stage(request cc_messages.DockerStagingRequestFromCC) error {
	compilerURL, err := stager.compilerDownloadURL(request)
	if err != nil {
		return err
	}

	actions := []models.ExecutorAction{}

	//Download tailor
	actions = append(
		actions,
		models.EmitProgressFor(
			models.ExecutorAction{
				models.DownloadAction{
					From:     compilerURL.String(),
					To:       path.Dir(TailorExecutablePath),
					CacheKey: "tailor-docker",
				},
			},
			"",
			"",
			"Failed to Download Tailor",
		),
	)

	var fileDescriptorLimit *uint64
	if request.FileDescriptors != 0 {
		fd := max(uint64(request.FileDescriptors), stager.config.MinFileDescriptors)
		fileDescriptorLimit = &fd
	}

	//Run Smelter
	actions = append(
		actions,
		models.EmitProgressFor(
			models.ExecutorAction{
				models.RunAction{
					Path:    TailorExecutablePath,
					Args:    []string{"-outputMetadataJSONFilename", TailorOutputPath, "-dockerRef", request.DockerImageUrl},
					Env:     request.Environment.BBSEnvironment(),
					Timeout: 15 * time.Minute,
					ResourceLimits: models.ResourceLimits{
						Nofile: fileDescriptorLimit,
					},
				},
			},
			"Staging...",
			"Staging Complete",
			"Staging Failed",
		),
	)

	annotationJson, _ := json.Marshal(models.StagingTaskAnnotation{
		AppId:  request.AppId,
		TaskId: request.TaskId,
	})

	task := receptor.TaskCreateRequest{
		ResultFile:            TailorOutputPath,
		TaskGuid:              taskGuid(request),
		Domain:                TaskDomain,
		Stack:                 request.Stack,
		MemoryMB:              int(max(uint64(request.MemoryMB), uint64(stager.config.MinMemoryMB))),
		DiskMB:                int(max(uint64(request.DiskMB), uint64(stager.config.MinDiskMB))),
		Actions:               actions,
		CompletionCallbackURL: stager.config.CallbackURL,
		Log: receptor.LogConfig{
			Guid:       request.AppId,
			SourceName: "STG",
		},
		Annotation: string(annotationJson),
	}

	stager.logger.Info("desiring-docker-task", lager.Data{
		"task_guid":    task.TaskGuid,
		"callback_url": stager.config.CallbackURL,
	})

	err = stager.diegoAPIClient.CreateTask(task)
	if receptorErr, ok := err.(receptor.Error); ok {
		if receptorErr.Type == receptor.TaskGuidAlreadyExists {
			err = nil
		}
	}

	return err
}

func (stager *stager_docker) compilerDownloadURL(request cc_messages.DockerStagingRequestFromCC) (*url.URL, error) {

	var circusFilename string
	if len(stager.config.DockerCircusPath) > 0 {
		circusFilename = stager.config.DockerCircusPath
	} else {
		circusFilename = DockerCircusFilename
	}
	parsed, err := url.Parse(circusFilename)
	if err != nil {
		return nil, errors.New("couldn't parse compiler URL")
	}

	switch parsed.Scheme {
	case "http", "https":
		return parsed, nil
	case "":
		break
	default:
		return nil, errors.New("wTF")
	}

	staticRoute, ok := router.NewFileServerRoutes().RouteForHandler(router.FS_STATIC)
	if !ok {
		return nil, errors.New("couldn't generate the compiler download path")
	}

	urlString := urljoiner.Join(stager.config.FileServerURL, staticRoute.Path, circusFilename)

	url, err := url.ParseRequestURI(urlString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse compiler download URL: %s", err)
	}

	return url, nil
}

func max(x, y uint64) uint64 {
	if x > y {
		return x
	} else {
		return y
	}
}

func taskGuid(request cc_messages.DockerStagingRequestFromCC) string {
	return fmt.Sprintf("%s-%s", request.AppId, request.TaskId)
}
