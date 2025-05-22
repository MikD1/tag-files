using MediatR;
using Microsoft.EntityFrameworkCore;
using TagFilesService.FilesProcessing.Contracts;
using TagFilesService.Infrastructure;
using TagFilesService.Model.Processing;

namespace TagFilesService.FilesProcessing.Handlers;

public class GetProcessingFilesHandler(AppDbContext dbContext)
    : IRequestHandler<GetProcessingFilesRequest, List<ProcessingFileDto>>
{
    public async Task<List<ProcessingFileDto>> Handle(GetProcessingFilesRequest request,
        CancellationToken cancellationToken)
    {
        List<ProcessingFile> processingFiles = await dbContext.ProcessingFiles
            .Where(x => x.Status != ProcessingStatus.Done && x.Status != ProcessingStatus.Failed)
            .OrderBy(x => x.Id)
            .ToListAsync(cancellationToken);

        return processingFiles.Select(ProcessingFileDto.FromModel).ToList();
    }
}