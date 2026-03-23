using MediatR;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;
using TagFilesService.Model.Processing;

namespace TagFilesService.Library.Handlers;

public class InitiateUploadHandler(IPresignedService presignedService, AppDbContext dbContext)
    : IRequestHandler<InitiateUploadRequest, Dictionary<string, string>>
{
    public async Task<Dictionary<string, string>> Handle(InitiateUploadRequest request,
        CancellationToken cancellationToken)
    {
        Dictionary<string, string> result = [];
        foreach (string fileName in request.FileNames)
        {
            string url = await presignedService.GenerateUploadUrl(
                Buckets.Temporary, fileName, (int)TimeSpan.FromHours(10).TotalSeconds);
            result.Add(fileName, url);

            ProcessingFile processingFile = new(fileName, request.CollectionId);
            await dbContext.AddAsync(processingFile, cancellationToken);
        }

        await dbContext.SaveChangesAsync(cancellationToken);
        return result;
    }
}