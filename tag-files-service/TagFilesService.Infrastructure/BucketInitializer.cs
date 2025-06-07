using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Minio;
using Minio.DataModel.Args;
using TagFilesService.Model;

namespace TagFilesService.Infrastructure;

public class BucketInitializer(ILogger<BucketInitializer> logger, IMinioClient minioClient)
    : IHostedService
{
    public async Task StartAsync(CancellationToken cancellationToken)
    {
        logger.LogInformation("Starting bucket initialization");

        string[] buckets = [Buckets.Library, Buckets.Thumbnail, Buckets.Temporary];
        foreach (string bucket in buckets)
        {
            bool exists = await minioClient
                .BucketExistsAsync(new BucketExistsArgs().WithBucket(bucket), cancellationToken);
            if (!exists)
            {
                logger.LogInformation("Creating bucket: {Bucket}", bucket);

                await minioClient.MakeBucketAsync(
                    new MakeBucketArgs().WithBucket(bucket),
                    cancellationToken);

                string policy = $@"{{
                    ""Version"": ""2012-10-17"",
                    ""Statement"": [
                        {{
                            ""Effect"": ""Allow"",
                            ""Principal"": {{""AWS"": [""*""]}},
                            ""Action"": [
                                ""s3:GetObject"",
                                ""s3:PutObject"",
                                ""s3:DeleteObject""
                            ],
                            ""Resource"": [""arn:aws:s3:::{bucket}/*""]
                        }}
                    ]
                }}";

                await minioClient.SetPolicyAsync(
                    new SetPolicyArgs()
                        .WithBucket(bucket)
                        .WithPolicy(policy),
                    cancellationToken);

                logger.LogInformation("Created bucket with public access: {Bucket}", bucket);
            }
            else
            {
                logger.LogInformation("Bucket already exists: {Bucket}", bucket);
            }
        }

        logger.LogInformation("Bucket initialization completed");
    }

    public Task StopAsync(CancellationToken cancellationToken)
    {
        return Task.CompletedTask;
    }
}