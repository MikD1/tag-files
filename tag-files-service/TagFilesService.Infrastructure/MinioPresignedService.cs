using Microsoft.Extensions.Configuration;
using Minio;
using Minio.DataModel.Args;
using TagFilesService.Model;

namespace TagFilesService.Infrastructure;

public class MinioPresignedService : IPresignedService
{
    public MinioPresignedService(IConfiguration configuration)
    {
        string endpoint = configuration["S3:PublicUrl"] ?? $"http://{configuration["S3:Url"]}";
        Uri uri = new(endpoint);
        _client = new MinioClient()
            .WithEndpoint(uri.Host, uri.Port)
            .WithCredentials(configuration["S3:Username"], configuration["S3:Password"])
            .WithSSL(uri.Scheme == "https")
            .Build();
    }

    public async Task<string> GenerateUploadUrl(string bucket, string key, int expirySeconds)
    {
        PresignedPutObjectArgs args = new PresignedPutObjectArgs()
            .WithBucket(bucket)
            .WithObject(key)
            .WithExpiry(expirySeconds);

        return await _client.PresignedPutObjectAsync(args);
    }

    private readonly IMinioClient _client;
}
