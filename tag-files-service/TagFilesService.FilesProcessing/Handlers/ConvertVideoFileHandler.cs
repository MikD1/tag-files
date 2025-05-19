using MediatR;
using TagFilesService.FilesProcessing.Contracts;

namespace TagFilesService.FilesProcessing.Handlers;

public class ConvertVideoFileHandler(VideoConversionQueue conversionQueue) : IRequestHandler<ConvertVideoFileRequest>
{
    public Task Handle(ConvertVideoFileRequest request, CancellationToken cancellationToken)
    {
        // 1. Check if file already converted
        // 2. Add convert task to queue
        // 3. Remove file from temporary bucket
        conversionQueue.Enqueue(request.FileName);
        return Task.CompletedTask;
    }
}