using MediatR;

namespace TagFilesService.Library.Contracts;

public record InitiateUploadRequest(
    List<string> FileNames,
    uint? CollectionId = null)
    : IRequest<Dictionary<string, string>>;
