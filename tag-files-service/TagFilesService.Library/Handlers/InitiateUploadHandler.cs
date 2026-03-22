using MediatR;
using Minio;
using Minio.DataModel.Args;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;
using TagFilesService.Model.Processing;

namespace TagFilesService.Library.Handlers;

public class InitiateUploadHandler(IMinioClient minio, AppDbContext dbContext)
    : IRequestHandler<InitiateUploadRequest, Dictionary<string, string>>
{
    public async Task<Dictionary<string, string>> Handle(InitiateUploadRequest request,
        CancellationToken cancellationToken)
    {
        Dictionary<string, string> result = [];
        foreach (string fileName in request.FileNames)
        {
            string url = await GeneratePresignedUrl(fileName);
            result.Add(fileName, url);

            ProcessingFile processingFile = new(fileName, request.CollectionId);
            await dbContext.AddAsync(processingFile, cancellationToken);
        }

        await dbContext.SaveChangesAsync(cancellationToken);
        return result;
    }

    private async Task<string> GeneratePresignedUrl(string fileName)
    {
        PresignedPutObjectArgs args = new PresignedPutObjectArgs()
            .WithBucket(Buckets.Temporary)
            .WithObject(fileName)
            .WithExpiry((int)TimeSpan.FromHours(10).TotalSeconds);

        return await minio.PresignedPutObjectAsync(args);
    }
}
