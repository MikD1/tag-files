using MediatR;
using Minio;
using Minio.DataModel.Args;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers;

public class GeneratePresignedUrlsHandler(IMinioClient minio)
    : IRequestHandler<GeneratePresignedUrlsRequest, Dictionary<string, string>>
{
    public async Task<Dictionary<string, string>> Handle(GeneratePresignedUrlsRequest request,
        CancellationToken cancellationToken)
    {
        Dictionary<string, string> result = [];
        foreach (string fileName in request.FileNames)
        {
            string url = await GeneratePresignedUrl(fileName);
            result.Add(fileName, url);
        }

        return result;
    }

    private async Task<string> GeneratePresignedUrl(string fileName)
    {
        PresignedPutObjectArgs args = new PresignedPutObjectArgs()
            .WithBucket(Buckets.Temporary)
            .WithObject(fileName)
            .WithExpiry((int)TimeSpan.FromHours(1).TotalSeconds);

        return await minio.PresignedPutObjectAsync(args);
    }
}