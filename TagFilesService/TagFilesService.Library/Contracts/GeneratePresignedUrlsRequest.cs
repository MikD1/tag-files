using MediatR;

namespace TagFilesService.Library.Contracts;

public record GeneratePresignedUrlsRequest(
    List<string> FileNames)
    : IRequest<Dictionary<string, string>>;