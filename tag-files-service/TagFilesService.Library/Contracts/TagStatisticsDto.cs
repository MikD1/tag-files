namespace TagFilesService.Library.Contracts;

public record TagStatisticsDto(
    string TagName,
    int UsageCount);