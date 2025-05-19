using MediatR;

namespace TagFilesService.FilesProcessing.Contracts;

public record ConvertVideoFileRequest(
    string FileName)
    : IRequest;