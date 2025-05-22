using MediatR;

namespace TagFilesService.FilesProcessing.Contracts;

public record FileProcessingRequest(
    string FileName,
    string ContentType)
    : IRequest;