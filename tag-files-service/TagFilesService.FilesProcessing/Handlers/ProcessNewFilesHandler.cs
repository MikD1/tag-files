using MediatR;
using TagFilesService.FilesProcessing.Contracts;

namespace TagFilesService.FilesProcessing.Handlers;

public class ProcessNewFilesHandler : IRequestHandler<ProcessNewFilesRequest>
{
    public Task Handle(ProcessNewFilesRequest request, CancellationToken cancellationToken)
    {
        // 1. Find files in 'temporary' bucket
        // 2. Add rows to 'ProcessingFiles' table
        // 3. Convert file
        // 4. Save converted file to 'library' bucket
        // 5. Add row to 'FilesMetadata' table
        // 6. Generate thumbnail
        return Task.CompletedTask;
    }
}