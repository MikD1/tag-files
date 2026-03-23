namespace TagFilesService.Model;

public interface IPresignedService
{
    Task<string> GenerateUploadUrl(string bucket, string key, int expirySeconds);
}
