using MediatR;

namespace TagFilesService.FilesProcessing.Contracts;

public record GetProcessingFilesRequest : IRequest<List<ProcessingFileDto>>;