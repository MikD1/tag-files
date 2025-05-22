using MediatR;
using TagFilesService.FilesProcessing.Contracts;
using TagFilesService.Infrastructure;
using TagFilesService.Model.Processing;

namespace TagFilesService.FilesProcessing.Handlers;

public class FileProcessingHandler(AppDbContext dbContext, FilesProcessingQueue processingQueue)
    : IRequestHandler<FileProcessingRequest>
{
    public async Task Handle(FileProcessingRequest request, CancellationToken cancellationToken)
    {
        ProcessingFile processingFile = new(request.FileName, request.ContentType);
        await dbContext.AddAsync(processingFile, cancellationToken);
        await dbContext.SaveChangesAsync(cancellationToken);

        await processingQueue.Enqueue(processingFile, cancellationToken);
    }
}