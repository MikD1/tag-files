using MediatR;
using TagFilesService.FilesProcessing.Contracts;

namespace TagFilesService.FilesProcessing.Handlers;

public class FileProcessingHandler(FilesProcessingQueue processingQueue) : IRequestHandler<FileProcessingRequest>
{
    public async Task Handle(FileProcessingRequest request, CancellationToken cancellationToken)
    {
        FileProcessingInfo fileInfo = new(request.FileName, request.ContentType);
        await processingQueue.Enqueue(fileInfo, cancellationToken);
    }
}