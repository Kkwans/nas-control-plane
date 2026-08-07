package agentsocket

import (
	"errors"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/protobuf/types/known/structpb"
)

func decodeProjectDeleteRequest(request *structpb.Struct) (docker.ProjectDeleteRequest, error) {
	if request == nil {
		return docker.ProjectDeleteRequest{}, errors.New("project delete request is required")
	}
	projectID, err := requiredString(request, "project_id")
	if err != nil {
		return docker.ProjectDeleteRequest{}, err
	}
	kind := docker.ProjectKindCompose
	if rawKind, ok := request.AsMap()["kind"].(string); ok && rawKind != "" {
		kind = docker.ProjectKind(rawKind)
	}
	result := docker.ProjectDeleteRequest{ProjectID: projectID, Kind: kind}
	values := request.AsMap()
	result.RegistryName = optionalString(values, "registry_name")
	if result.RegistryName == "" {
		result.RegistryName = optionalString(values, "name")
	}
	if _, err := result.Normalize(); err != nil {
		return docker.ProjectDeleteRequest{}, err
	}
	return result, nil
}

func optionalString(values map[string]any, key string) string {
	value, ok := values[key].(string)
	if !ok {
		return ""
	}
	return value
}
