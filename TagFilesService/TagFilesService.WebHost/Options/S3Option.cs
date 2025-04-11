namespace TagFilesService.WebHost.Options;

public class S3Option
{
    public required string Host { get; init; }

    public required string AccessKey { get; init; }

    public required string SecretKey { get; init; }
}
